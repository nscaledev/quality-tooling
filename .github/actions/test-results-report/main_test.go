package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGrafanaQueryPlanningModeWritesPlanOutputsWithoutMCP(t *testing.T) {
	previousPlanner := runGrafanaLogQueryPlanning
	plannerCalled := false
	runGrafanaLogQueryPlanning = func(_ context.Context, receivedConfig Config, analysis Analysis) ([]GrafanaLogPlannedQuery, error) {
		plannerCalled = true
		if !receivedConfig.EnableGrafanaLogs || !receivedConfig.EnableAIAnalysis || receivedConfig.ClaudeToken == "" {
			t.Fatalf("planner received unexpected config: %+v", receivedConfig)
		}
		if len(analysis.Failures) != 1 || analysis.Failures[0].Name != "button color" {
			t.Fatalf("planner received unexpected failures: %+v", analysis.Failures)
		}
		return []GrafanaLogPlannedQuery{}, nil
	}
	defer func() {
		runGrafanaLogQueryPlanning = previousPlanner
	}()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	if err := os.WriteFile(resultsPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="UI" tests="1" failures="1">
  <testsuite name="chromium" tests="1" failures="1">
    <testcase classname="visual" name="button color">
      <failure message="expected #0055ff, got #0044dd">visual assertion failed</failure>
    </testcase>
  </testsuite>
</testsuites>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}

	planPath := filepath.Join(tempDir, "grafana-plan.json")
	outputPath := filepath.Join(tempDir, "github-output")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	err := runGrafanaQueryPlanningMode(context.Background(), Config{
		TestResultsPath:       resultsPath,
		Format:                formatJUnit,
		EnableGrafanaLogs:     true,
		EnableAIAnalysis:      true,
		ClaudeToken:           "test-token",
		GrafanaLogMaxFailures: 2,
		GrafanaQueryPlanPath:  planPath,
	})
	if err != nil {
		t.Fatalf("runGrafanaQueryPlanningMode returned error: %v", err)
	}
	if !plannerCalled {
		t.Fatal("expected planner to run")
	}

	plan := readPlainTestFile(t, planPath)
	if !strings.Contains(plan, `"queries": []`) {
		t.Fatalf("expected empty deterministic query plan, got:\n%s", plan)
	}
	outputs := readPlainTestFile(t, outputPath)
	for _, expected := range []string{
		"plan-path=" + planPath,
		"query-count=0",
		"needs-mcp=false",
	} {
		if !strings.Contains(outputs, expected) {
			t.Fatalf("outputs missing %q:\n%s", expected, outputs)
		}
	}
}

func TestRunGrafanaQueryPlanningModeFailsOpenOnPlannerError(t *testing.T) {
	previousPlanner := runGrafanaLogQueryPlanning
	runGrafanaLogQueryPlanning = func(_ context.Context, _ Config, _ Analysis) ([]GrafanaLogPlannedQuery, error) {
		return nil, errors.New("claude unavailable")
	}
	defer func() {
		runGrafanaLogQueryPlanning = previousPlanner
	}()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	if err := os.WriteFile(resultsPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="API" tests="1" failures="1">
  <testsuite name="region" tests="1" failures="1">
    <testcase classname="network" name="creates network">
      <failure message="network entered error state">backend-shaped failure</failure>
    </testcase>
  </testsuite>
</testsuites>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}

	planPath := filepath.Join(tempDir, "grafana-plan.json")
	outputPath := filepath.Join(tempDir, "github-output")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	err := runGrafanaQueryPlanningMode(context.Background(), Config{
		TestResultsPath:      resultsPath,
		Format:               formatJUnit,
		EnableGrafanaLogs:    true,
		EnableAIAnalysis:     true,
		ClaudeToken:          "test-token",
		GrafanaQueryPlanPath: planPath,
	})
	if err != nil {
		t.Fatalf("runGrafanaQueryPlanningMode should fail open on planner errors, got: %v", err)
	}

	plan := readPlainTestFile(t, planPath)
	if !strings.Contains(plan, `"queries": []`) {
		t.Fatalf("expected planner error to write empty plan, got:\n%s", plan)
	}
	outputs := readPlainTestFile(t, outputPath)
	if !strings.Contains(outputs, "needs-mcp=false") {
		t.Fatalf("expected planner error to skip MCP setup, got:\n%s", outputs)
	}
}

func readPlainTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
