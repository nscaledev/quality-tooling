package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeCommandUsesPinnedPackage(t *testing.T) {
	// Every Claude operation must use the reviewed CLI version, never npm latest.
	cmd := newClaudeCommand(context.Background(), "test-token", "test prompt", "test input")
	want := []string{"npx", "--yes", "@anthropic-ai/claude-code@2.1.285", "-p", "test prompt"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Claude command arguments = %q, want %q", cmd.Args, want)
	}
}

func TestRunReportsAIStatusWithoutLosingTestResults(t *testing.T) {
	// An optional CLI failure must be visible without dropping the parsed test report.
	for _, test := range []struct {
		name       string
		enabled    bool
		failedTest bool
		cliFails   bool
		status     string
	}{
		{name: "stdout-only CLI failure", enabled: true, failedTest: true, cliFails: true, status: "failed"},
		{name: "completed analysis", enabled: true, failedTest: true, status: "completed"},
		{name: "disabled analysis", failedTest: true, status: "disabled"},
		{name: "nothing to analyze", enabled: true, status: "skipped (no failed or skipped tests)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\ncat >/dev/null\n"
			if test.cliFails {
				script += "printf 'OAuth rejected: %s\\n::error::not a workflow command\\n' \"$CLAUDE_CODE_OAUTH_TOKEN\"\nexit 1\n"
			} else {
				script += "printf '## Test Failure Analysis\\n\\nMock analysis of the test failure.\\n'\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GITHUB_OUTPUT", filepath.Join(dir, "outputs"))
			results := `<testsuites><testsuite name="console"><testcase name="sample test">`
			if test.failedTest {
				results += `<failure message="button missing">expected visible button</failure>`
			}
			results += `</testcase></testsuite></testsuites>`
			resultsPath := filepath.Join(dir, "results.xml")
			if err := os.WriteFile(resultsPath, []byte(results), 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(dir, "stderr")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			originalStderr := os.Stderr
			os.Stderr = log
			t.Cleanup(func() { os.Stderr = originalStderr; _ = log.Close() })
			summaryPath := filepath.Join(dir, "summary.md")
			err = run(context.Background(), Config{
				TestResultsPath:  resultsPath,
				Format:           formatJUnit,
				WriteStepSummary: true,
				StepSummaryPath:  summaryPath,
				EnableAIAnalysis: test.enabled,
				ClaudeToken:      "test-private-credential",
				MaxFailures:      10,
				MaxSkips:         10,
			})
			if err != nil {
				t.Fatalf("optional AI outcome must preserve reporting: %v", err)
			}
			summary := readPlainTestFile(t, summaryPath)
			if !strings.Contains(summary, "**AI analysis:** "+test.status+".") {
				t.Fatalf("missing AI status: %s", summary)
			}
			outputs := readPlainTestFile(t, filepath.Join(dir, "outputs"))
			if !strings.Contains(outputs, "total=1") {
				t.Fatalf("missing normal test outputs: %s", outputs)
			}
			logs := readPlainTestFile(t, logPath)
			if test.cliFails {
				for _, expected := range []string{"::warning title=AI failure analysis failed::", "exit status 1", "OAuth rejected"} {
					if !strings.Contains(logs, expected) {
						t.Errorf("missing diagnostic %q: %s", expected, logs)
					}
				}
				if strings.Count(logs, "::warning title=") != 1 || strings.Contains(logs, "\n::error::") || strings.Contains(logs, "test-private-credential") {
					t.Errorf("unsafe or duplicate warning: %s", logs)
				}
				if !strings.Contains(summary, "### Failed Tests") || !strings.Contains(summary, "sample test") || strings.Contains(summary, "OAuth rejected") {
					t.Errorf("fallback must retain tests and keep CLI output in logs: %s", summary)
				}
			} else if strings.Contains(logs, "::warning title=AI failure analysis failed::") {
				t.Errorf("unexpected failure annotation: %s", logs)
			}
		})
	}
}
