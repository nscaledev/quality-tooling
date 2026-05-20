package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJUnitReport(t *testing.T) {
	t.Parallel()

	input := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="Console E2E Test Suites" tests="3" failures="1" skipped="1" time="12.5">
  <testsuite name="chromium" tests="3" failures="1" skipped="1" time="12.5">
    <testcase classname="settings.organisation" name="creates organisation group" time="1.2"/>
    <testcase classname="compute.instance" name="creates instance" time="8.5">
      <failure message="Expected button to be visible">TimeoutError: locator.click timed out
        at src/spec/compute/instance.spec.ts:42:11</failure>
      <system-out>console logs here</system-out>
    </testcase>
    <testcase classname="network.vpc" name="deletes VPC" time="0">
      <skipped message="feature flag disabled"/>
    </testcase>
  </testsuite>
</testsuites>`)

	run, err := parseJUnit(input)
	if err != nil {
		t.Fatalf("parseJUnit returned error: %v", err)
	}

	if run.Name != "Console E2E Test Suites" {
		t.Fatalf("run name = %q", run.Name)
	}
	if len(run.Tests) != 3 {
		t.Fatalf("test count = %d", len(run.Tests))
	}

	stats := calculateStats(run.Tests)
	if stats.Total != 3 || stats.Passed != 1 || stats.Failed != 1 || stats.Skipped != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	failed := run.Tests[1]
	if failed.Status != StatusFailed {
		t.Fatalf("failed status = %q", failed.Status)
	}
	if failed.ID != "compute.instance::creates instance" {
		t.Fatalf("failed id = %q", failed.ID)
	}
	if !strings.Contains(failed.Message, "Expected button") {
		t.Fatalf("failed message = %q", failed.Message)
	}
	if failed.Duration != 8500*time.Millisecond {
		t.Fatalf("failed duration = %s", failed.Duration)
	}

	skipped := run.Tests[2]
	if skipped.Status != StatusSkipped || skipped.Message != "feature flag disabled" {
		t.Fatalf("unexpected skipped test: %+v", skipped)
	}
}

func TestParsePlaywrightJSONReport(t *testing.T) {
	t.Parallel()

	input := readFixture(t, "playwright-results.json")

	run, err := parsePlaywrightJSON(input)
	if err != nil {
		t.Fatalf("parsePlaywrightJSON returned error: %v", err)
	}

	if len(run.Tests) != 3 {
		t.Fatalf("test count = %d", len(run.Tests))
	}

	stats := calculateStats(run.Tests)
	if stats.Total != 3 || stats.Passed != 1 || stats.Failed != 1 || stats.Skipped != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if run.Duration != 421938855*time.Microsecond {
		t.Fatalf("duration = %s", run.Duration)
	}

	failed := firstTestWithStatus(t, run.Tests, StatusFailed)
	if failed.ID != "spec/compute/instance.spec.ts > Instance Management::should create and delete instance successfully::chromium" {
		t.Fatalf("failed id = %q", failed.ID)
	}
	if failed.File != "spec/compute/instance.spec.ts" {
		t.Fatalf("failed file = %q", failed.File)
	}
	if failed.Line != 88 {
		t.Fatalf("failed line = %d", failed.Line)
	}
	if !strings.Contains(failed.Message, "TimeoutError") {
		t.Fatalf("failed message = %q", failed.Message)
	}
	if !strings.Contains(failed.Output, "Network Logs") {
		t.Fatalf("failed output = %q", failed.Output)
	}

	skipped := firstTestWithStatus(t, run.Tests, StatusSkipped)
	if skipped.ID != "spec/navigation/private-nav.spec.ts > Private Cloud - Navigation::NAV-03 - feature-flagged nav items hidden when flags disabled::chromium" {
		t.Fatalf("skipped id = %q", skipped.ID)
	}
}

func TestParsePlaywrightJSONTreatsFlakyAsPassingFinalStatus(t *testing.T) {
	t.Parallel()

	input := []byte(`{
  "suites": [{
    "title": "spec/settings.spec.ts",
    "file": "spec/settings.spec.ts",
    "specs": [{
      "title": "saves settings",
      "tests": [{
        "projectName": "chromium",
        "status": "flaky",
        "results": [
          {"status": "failed", "duration": 1000, "error": {"message": "first attempt failed"}},
          {"status": "passed", "duration": 500}
        ]
      }]
    }]
  }]
}`)

	run, err := parsePlaywrightJSON(input)
	if err != nil {
		t.Fatalf("parsePlaywrightJSON returned error: %v", err)
	}

	stats := calculateStats(run.Tests)
	if stats.Passed != 1 || stats.Failed != 0 {
		t.Fatalf("unexpected stats for flaky final status: %+v", stats)
	}
}

func TestParseGinkgoJSONReport(t *testing.T) {
	t.Parallel()

	input := readFixture(t, "ginkgo-results.json")

	run, err := parseGinkgoJSON(input)
	if err != nil {
		t.Fatalf("parseGinkgoJSON returned error: %v", err)
	}

	if run.Name != "API Test Suites" {
		t.Fatalf("run name = %q", run.Name)
	}
	stats := calculateStats(run.Tests)
	if stats.Total != 2 || stats.Passed != 1 || stats.Failed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	failed := run.Tests[1]
	if failed.ID != "Core Cluster Management > When creating clusters > creates a cluster" {
		t.Fatalf("failed id = %q", failed.ID)
	}
	if failed.File != "cluster_test.go" || failed.Line != 123 {
		t.Fatalf("failed location = %s:%d", failed.File, failed.Line)
	}
}

func TestParseUniRegionJUnitFixture(t *testing.T) {
	t.Parallel()

	run, err := parseJUnit(readFixture(t, "uni-region-junit.xml"))
	if err != nil {
		t.Fatalf("parseJUnit returned error: %v", err)
	}

	if run.Name != "JUnit Test Results" {
		t.Fatalf("run name = %q", run.Name)
	}
	if len(run.Tests) != 3 {
		t.Fatalf("test count = %d", len(run.Tests))
	}

	stats := calculateStats(run.Tests)
	if stats.Total != 3 || stats.Passed != 1 || stats.Failed != 1 || stats.Skipped != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if run.Duration != 224280312214*time.Nanosecond {
		t.Fatalf("duration = %s", run.Duration)
	}

	failed := firstTestWithStatus(t, run.Tests, StatusFailed)
	if !strings.Contains(failed.Message, "deleting filestorage") {
		t.Fatalf("failed message = %q", failed.Message)
	}
	if !strings.Contains(failed.Message, "/home/runner/work/uni-region/uni-region/test/api/suites/filestorage_test.go:620") {
		t.Fatalf("failed message does not preserve source location: %q", failed.Message)
	}
	if failed.File != "/home/runner/work/uni-region/uni-region/test/api/suites/filestorage_test.go" || failed.Line != 620 {
		t.Fatalf("failed location = %s:%d", failed.File, failed.Line)
	}

	skipped := firstTestWithStatus(t, run.Tests, StatusSkipped)
	if !strings.Contains(skipped.Message, "INST-457") {
		t.Fatalf("skipped message = %q", skipped.Message)
	}
}

func TestExtractLocation(t *testing.T) {
	t.Parallel()

	file, line := extractLocation("TimeoutError at src/spec/compute/instance.spec.ts:42:11")
	if file != "src/spec/compute/instance.spec.ts" || line != 42 {
		t.Fatalf("location = %s:%d", file, line)
	}

	file, line = extractLocation("no source location here")
	if file != "" || line != 0 {
		t.Fatalf("unexpected location = %s:%d", file, line)
	}
}

func TestAnalyzeWithPreviousResults(t *testing.T) {
	t.Parallel()

	current := TestRun{Duration: 12 * time.Second, Tests: []TestCase{
		{ID: "passes-now", Name: "passes now", Status: StatusPassed},
		{ID: "new-failure", Name: "new failure", Status: StatusFailed},
		{ID: "recurring-failure", Name: "recurring failure", Status: StatusFailed},
		{ID: "new-skip", Name: "new skip", Status: StatusSkipped},
		{ID: "recurring-skip", Name: "recurring skip", Status: StatusSkipped},
	}}
	previous := TestRun{Duration: 10 * time.Second, Tests: []TestCase{
		{ID: "passes-now", Name: "passes now", Status: StatusFailed},
		{ID: "recurring-failure", Name: "recurring failure", Status: StatusFailed},
		{ID: "resolved-failure", Name: "resolved failure", Status: StatusFailed},
		{ID: "recurring-skip", Name: "recurring skip", Status: StatusSkipped},
		{ID: "resolved-skip", Name: "resolved skip", Status: StatusSkipped},
	}}

	analysis := analyze(current, &previous)
	if analysis.Compare == nil {
		t.Fatal("expected comparison")
	}

	if len(analysis.Compare.NewFailures) != 1 || analysis.Compare.NewFailures[0].ID != "new-failure" {
		t.Fatalf("new failures = %+v", analysis.Compare.NewFailures)
	}
	if len(analysis.Compare.RecurringFailures) != 1 || analysis.Compare.RecurringFailures[0].ID != "recurring-failure" {
		t.Fatalf("recurring failures = %+v", analysis.Compare.RecurringFailures)
	}
	if len(analysis.Compare.ResolvedFailures) != 2 {
		t.Fatalf("resolved failures = %+v", analysis.Compare.ResolvedFailures)
	}
	if len(analysis.Compare.NewSkips) != 1 || len(analysis.Compare.RecurringSkips) != 1 || len(analysis.Compare.ResolvedSkips) != 1 {
		t.Fatalf("skip comparison = %+v", analysis.Compare)
	}
	if analysis.Compare.DurationDelta != 2*time.Second {
		t.Fatalf("duration delta = %s", analysis.Compare.DurationDelta)
	}
}

func TestCompareRunsHandlesDuplicateIDsByOutcome(t *testing.T) {
	t.Parallel()

	current := TestRun{Tests: []TestCase{
		{ID: "retry-case", Name: "retry case first attempt", Status: StatusFailed},
		{ID: "retry-case", Name: "retry case retry", Status: StatusPassed},
		{ID: "skip-case", Name: "skip case first attempt", Status: StatusSkipped},
		{ID: "skip-case", Name: "skip case retry", Status: StatusPassed},
	}}
	previous := TestRun{Tests: []TestCase{
		{ID: "retry-case", Name: "retry case previous attempt", Status: StatusFailed},
		{ID: "retry-case", Name: "retry case previous retry", Status: StatusPassed},
		{ID: "skip-case", Name: "skip case previous attempt", Status: StatusSkipped},
		{ID: "skip-case", Name: "skip case previous retry", Status: StatusPassed},
	}}

	comparison := compareRuns(current, previous)

	if len(comparison.RecurringFailures) != 1 || comparison.RecurringFailures[0].ID != "retry-case" {
		t.Fatalf("recurring failures = %+v", comparison.RecurringFailures)
	}
	if len(comparison.NewFailures) != 0 || len(comparison.ResolvedFailures) != 0 {
		t.Fatalf("failure comparison should not be order-dependent: %+v", comparison)
	}
	if len(comparison.RecurringSkips) != 1 || comparison.RecurringSkips[0].ID != "skip-case" {
		t.Fatalf("recurring skips = %+v", comparison.RecurringSkips)
	}
	if len(comparison.NewSkips) != 0 || len(comparison.ResolvedSkips) != 0 {
		t.Fatalf("skip comparison should not be order-dependent: %+v", comparison)
	}
}

func TestMarkdownSummaryIncludesFailuresSkipsAndComparison(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Current: TestRun{Name: "Console E2E", Duration: 12 * time.Second},
		Stats:   Stats{Total: 3, Passed: 1, Failed: 1, Skipped: 1},
		Failures: []TestCase{{
			ID:      "failed-test",
			Suite:   "compute.instance",
			Name:    "creates instance",
			File:    "src/spec/compute/instance.spec.ts",
			Message: "Expected button to be visible",
		}},
		Skipped: []TestCase{{
			ID:      "skipped-test",
			Suite:   "network.vpc",
			Name:    "deletes VPC",
			Message: "feature flag disabled",
		}},
		Compare: &Comparison{
			NewFailures:       []TestCase{{ID: "failed-test", Name: "creates instance"}},
			RecurringFailures: []TestCase{{ID: "old-failure", Name: "still failing"}},
			ResolvedFailures:  []TestCase{{ID: "resolved", Name: "now passing"}},
			NewSkips:          []TestCase{{ID: "skipped-test", Name: "deletes VPC"}},
		},
	}

	markdown := renderStepSummary(analysis, RenderOptions{
		Title:        "E2E Test Results",
		Environment:  "dev",
		WorkflowURL:  "https://github.example/run",
		ReportURL:    "https://reports.example/allure",
		MaxFailures:  5,
		MaxSkips:     5,
		IncludeSkips: true,
	})

	for _, expected := range []string{
		"## E2E Test Results",
		"| Total | Passed | Failed | Skipped | Duration |",
		"### Previous Result Comparison",
		"New failures",
		"### Failed Tests",
		"creates instance",
		"### Skipped Tests",
		"feature flag disabled",
		"https://reports.example/allure",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, markdown)
		}
	}
}

func TestDetectFormatAndParseAuto(t *testing.T) {
	t.Parallel()

	junit := []byte(`<testsuite name="unit"><testcase classname="pkg" name="passes"/></testsuite>`)
	playwright := []byte(`{"suites":[{"title":"example.spec.ts","file":"example.spec.ts","specs":[{"title":"passes","tests":[{"projectName":"chromium","status":"expected","results":[{"status":"passed"}]}]}]}]}`)
	ginkgo := []byte(`[{"SuiteDescription":"api","SpecReports":[{"LeafNodeText":"passes","State":"passed"}]}]`)

	for name, tc := range map[string]struct {
		data       []byte
		wantFormat string
	}{
		"junit":      {data: junit, wantFormat: "junit"},
		"playwright": {data: playwright, wantFormat: "playwright-json"},
		"ginkgo":     {data: ginkgo, wantFormat: "ginkgo-json"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			format, err := detectFormat(tc.data)
			if err != nil {
				t.Fatalf("detectFormat returned error: %v", err)
			}
			if format != tc.wantFormat {
				t.Fatalf("format = %q, want %q", format, tc.wantFormat)
			}

			run, err := parseTestResults(tc.data, "auto")
			if err != nil {
				t.Fatalf("parseTestResults returned error: %v", err)
			}
			if len(run.Tests) != 1 || run.Tests[0].Status != StatusPassed {
				t.Fatalf("unexpected parsed run: %+v", run)
			}
		})
	}
}

func TestResolveResultsPathFiltersDirectoryByRequestedFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "results.json")
	xmlPath := filepath.Join(dir, "results.xml")

	if err := os.WriteFile(jsonPath, []byte(`{"suites":[]}`), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if err := os.WriteFile(xmlPath, []byte(`<testsuite name="unit"></testsuite>`), 0o600); err != nil {
		t.Fatalf("write xml: %v", err)
	}

	resolved, err := resolveResultsPath(dir, "junit")
	if err != nil {
		t.Fatalf("resolveResultsPath returned error: %v", err)
	}
	if resolved != xmlPath {
		t.Fatalf("resolved path = %q, want %q", resolved, xmlPath)
	}

	resolved, err = resolveResultsPath(dir, "playwright-json")
	if err != nil {
		t.Fatalf("resolveResultsPath returned error: %v", err)
	}
	if resolved != jsonPath {
		t.Fatalf("resolved path = %q, want %q", resolved, jsonPath)
	}
}

func TestBuildSlackPayloadIncludesContextButtonsAndAnalysis(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Total: 2, Passed: 1, Failed: 1},
		Failures: []TestCase{{
			Name:    "creates instance",
			Suite:   "compute.instance",
			Message: "Expected button to be visible",
		}},
		Compare: &Comparison{NewFailures: []TestCase{{Name: "creates instance"}}},
	}

	payload := buildSlackPayload(analysis, SlackOptions{
		Title:       "E2E Test Results",
		Environment: "dev",
		Branch:      "feat/e2e",
		Actor:       "octocat",
		WorkflowURL: "https://github.example/run",
		ReportURL:   "https://reports.example/allure",
		AIAnalysis:  "The failure is isolated to instance creation.",
		MaxFailures: 5,
	})

	if !strings.Contains(payload.Text, "E2E Test Results (DEV)") {
		t.Fatalf("text = %q", payload.Text)
	}
	rendered := slackPayloadText(payload)
	for _, expected := range []string{
		"Failed",
		"New failures",
		"creates instance",
		"Failure Analysis",
		"feat/e2e",
		"octocat",
		"GitHub Build",
		"Allure Report",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("payload missing %q:\n%s", expected, rendered)
		}
	}
}

func TestBuildSlackPayloadOmitsFailureDetailsWhenAIAnalysisSummarisesPatterns(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Current: TestRun{Name: "Region API Test Suites"},
		Stats:   Stats{Total: 67, Passed: 7, Failed: 23, Skipped: 37},
		Failures: []TestCase{{
			Name:    "Flavor Discovery > should return all available flavors",
			Suite:   "Flavor Discovery",
			File:    "/home/runner/work/uni-region/uni-region/test/api/suites/regions_test.go",
			Line:    139,
			Message: "status code 401: token is invalid or has expired",
		}},
	}

	payload := buildSlackPayload(analysis, SlackOptions{
		Title:              "Region API Test Results",
		Environment:        "dev",
		AIAnalysis:         "Auth/config issue: 23 failures and 37 skips appear blocked by 401 responses.",
		OmitFailureDetails: true,
	})

	rendered := slackPayloadText(payload)
	for _, unexpected := range []string{
		"Flavor Discovery > should return all available flavors",
		"test/api/suites/regions_test.go:139",
		"token is invalid or has expired",
	} {
		if strings.Contains(rendered, unexpected) {
			t.Fatalf("payload should omit raw failure detail %q:\n%s", unexpected, rendered)
		}
	}
	for _, expected := range []string{
		"Region API Test Results (DEV)",
		"Failed",
		"Failure Analysis",
		"Auth/config issue",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("payload missing %q:\n%s", expected, rendered)
		}
	}
}

func TestBuildSlackPayloadUsesLegacyFailureDetailsWhenAIAnalysisIsUnavailable(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Current: TestRun{Name: "Region API Test Suites"},
		Stats:   Stats{Total: 2, Passed: 1, Failed: 1},
		Failures: []TestCase{{
			Name:    "Flavor Discovery > should return all available flavors",
			Suite:   "Flavor Discovery",
			File:    "/home/runner/work/uni-region/uni-region/test/api/suites/regions_test.go",
			Line:    139,
			Message: "status code 401: token is invalid or has expired",
		}},
	}

	payload := buildSlackPayload(analysis, SlackOptions{
		Title:       "Region API Test Results",
		Environment: "dev",
		MaxFailures: 5,
	})

	rendered := slackPayloadText(payload)
	for _, expected := range []string{
		"*Failed Tests:*",
		"*Test:* Flavor Discovery > should return all available flavors",
		"*Suite:* `Flavor Discovery`",
		"*Location:* `regions_test.go:139`",
		"*Error:*\n```\nstatus code 401: token is invalid or has expired\n```",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("payload missing legacy failure detail %q:\n%s", expected, rendered)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	config := configFromEnv(map[string]string{
		"INPUT_TEST_RESULTS_PATH": "results.xml",
	})

	if config.Format != "auto" {
		t.Fatalf("format = %q", config.Format)
	}
	if config.Title != "Test Results" {
		t.Fatalf("title = %q", config.Title)
	}
	if !config.WriteStepSummary {
		t.Fatal("write step summary should default true")
	}
	if config.MaxFailures != 5 || config.MaxSkips != 10 {
		t.Fatalf("limits = failures %d skips %d", config.MaxFailures, config.MaxSkips)
	}
	if config.PreviousResultsSource != "path" {
		t.Fatalf("previous source = %q", config.PreviousResultsSource)
	}
	if config.SendSlack {
		t.Fatal("send slack should default false when no credentials are configured")
	}
}

func TestClaudePromptRequestsPatternSummary(t *testing.T) {
	t.Parallel()

	prompt := claudePrompt()
	for _, expected := range []string{
		"3-5 short Slack mrkdwn bullet lines",
		"Classify each pattern as one of: infra/external, code/core logic, test/false failure, unknown/mixed",
		"Each triage bullet must start with '- *<suite/category>* (<category>):'",
		"Group by suite name when one suite is affected",
		"For intentional or sentinel test failures",
		"remove or disable them before review",
		"test-level failure reasons are available in the GitHub build summary",
		"Do not list every failed or skipped test",
		"- *Auth / all suites* (infra/external):",
		aiSlackDelimiter,
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}

func TestRenderAIInputIncludesSkippedTests(t *testing.T) {
	t.Parallel()

	input := renderAIInputWithOptions(Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Passed: 1, Failed: 1, Skipped: 1},
		Failures: []TestCase{{
			Name:    "creates instance",
			Message: "timeout",
		}},
		Skipped: []TestCase{{
			Name:    "deletes VPC",
			Message: "feature flag disabled",
		}},
	}, AIInputOptions{})

	for _, expected := range []string{
		"Failed tests:",
		"Skipped tests:",
		"creates instance",
		"deletes VPC",
		"feature flag disabled",
	} {
		if !strings.Contains(input, expected) {
			t.Fatalf("AI input missing %q:\n%s", expected, input)
		}
	}
}

func slackPayloadText(payload SlackPayload) string {
	var parts []string
	parts = append(parts, payload.Text)
	for _, block := range payload.Blocks {
		if block.Text != nil {
			parts = append(parts, block.Text.Text)
		}
		for _, field := range block.Fields {
			parts = append(parts, field.Text)
		}
		for _, element := range block.Elements {
			if element.Text != nil {
				parts = append(parts, element.Text.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func firstTestWithStatus(t *testing.T, tests []TestCase, status TestStatus) TestCase {
	t.Helper()

	for _, test := range tests {
		if test.Status == status {
			return test
		}
	}
	t.Fatalf("no test with status %s in %+v", status, tests)
	return TestCase{}
}
