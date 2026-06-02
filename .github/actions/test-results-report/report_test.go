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

func TestMarkdownSummaryIncludesGrafanaLookupMetadata(t *testing.T) {
	t.Parallel()

	markdown := renderStepSummary(Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Total: 1, Failed: 1},
		GrafanaLogs: &GrafanaLogEnrichment{
			DatasourceUID:  "loki-dev",
			DatasourceName: "Loki",
			StartRFC3339:   "2026-06-01T13:00:00Z",
			EndRFC3339:     "2026-06-01T14:00:00Z",
			Contexts: []GrafanaLogContext{{
				QueryLabel:        "AI-planned backend query",
				FailureRef:        "f1",
				TestName:          "uploads file",
				BackendArea:       "file-storage",
				ExpectedError:     "POST /api/storage returned 500 for claim-123",
				SearchTerms:       []string{"claim-123", "file-storage", "500"},
				Confidence:        "medium",
				Query:             `{namespace=~".+"} |~ "(?i)(claim-123|file-storage|500)"`,
				GrafanaExploreURL: "https://grafana.example.com/explore?panes=encoded",
				Reason:            "The UI file upload failed after a backend storage API 500.",
				LineCount:         1,
				FilteredLineCount: 2,
				Entries: []GrafanaLogEntry{{
					Timestamp: "1780322400000000000",
					Line:      "file-storage controller failed claim-123 with backend 500",
					Labels: map[string]string{
						"app":       "file-storage-api",
						"namespace": "file-storage",
					},
				}},
			}},
		},
	}, RenderOptions{
		Title:           "E2E Test Results",
		OmitTestDetails: true,
	})

	for _, expected := range []string{
		"### Grafana Observations",
		"datasource `Loki` (`loki-dev`)",
		"time range `2026-06-01T13:00:00Z` to `2026-06-01T14:00:00Z`",
		"| uploads file | file-storage | 1 matching log line returned; components: file-storage-api",
		"[Open Grafana](https://grafana.example.com/)",
		"filtered 2 Grafana/MCP self-observability line(s)",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, markdown)
		}
	}
	for _, unexpected := range []string{
		"### Grafana Log Context",
		"Exact failure error:",
		"Search terms:",
		`{namespace=~".+"}`,
		"panes=encoded",
		"/explore?",
		"file-storage controller failed claim-123 with backend 500",
	} {
		if strings.Contains(markdown, unexpected) {
			t.Fatalf("summary should not include %q:\n%s", unexpected, markdown)
		}
	}
}

func TestRenderStepSummarySanitizesGrafanaLookupErrors(t *testing.T) {
	t.Parallel()

	markdown := renderStepSummary(Analysis{
		Current: TestRun{Name: "API Tests"},
		Stats:   Stats{Failed: 1, Total: 1},
		GrafanaLogs: &GrafanaLogEnrichment{
			Contexts: []GrafanaLogContext{{
				TestName:          "uploads file",
				BackendArea:       "file-storage",
				GrafanaExploreURL: "https://grafana.example.com/explore?panes=claim-123",
				Error:             `grafana MCP tool query_loki_logs failed for {namespace=~".+"} |~ "claim-123": datasource proxy returned 400`,
			}},
		},
	}, RenderOptions{Title: "E2E Test Results"})

	for _, expected := range []string{
		"### Grafana Observations",
		"Lookup failed; details are available in the job logs",
		"[Open Grafana](https://grafana.example.com/)",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, markdown)
		}
	}
	for _, unexpected := range []string{
		"query_loki_logs failed",
		`{namespace=~".+"}`,
		"claim-123",
		"panes=",
		"/explore?",
	} {
		if strings.Contains(markdown, unexpected) {
			t.Fatalf("summary should not include %q:\n%s", unexpected, markdown)
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

func TestBuildSlackPayloadKeepsLongerAIAnalysisForSlack(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Total: 4, Passed: 1, Failed: 3},
	}
	longAnalysis := strings.Repeat("a", 1300) + " marker-after-old-limit"

	payload := buildSlackPayload(analysis, SlackOptions{
		Title:      "E2E Test Results",
		AIAnalysis: longAnalysis,
	})

	rendered := slackPayloadText(payload)
	if !strings.Contains(rendered, "marker-after-old-limit") {
		t.Fatalf("payload truncated AI analysis at the old short limit:\n%s", rendered)
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
	if config.MaxFailures != 10 || config.MaxSkips != 10 {
		t.Fatalf("limits = failures %d skips %d", config.MaxFailures, config.MaxSkips)
	}
	if config.PreviousResultsSource != "path" {
		t.Fatalf("previous source = %q", config.PreviousResultsSource)
	}
	if config.SendSlack {
		t.Fatal("send slack should default false when no credentials are configured")
	}
	if config.EnableGrafanaLogs {
		t.Fatal("grafana log enrichment should default false")
	}
	if config.GrafanaOrgID != "1" {
		t.Fatalf("grafana org ID default = %q", config.GrafanaOrgID)
	}
	if config.GrafanaLokiName != "Loki" {
		t.Fatalf("grafana Loki datasource name default = %q", config.GrafanaLokiName)
	}
	if config.GrafanaLogLookback != "2h" || config.GrafanaLogLimit != 20 || config.GrafanaLogMaxFailures != 6 || config.GrafanaLogConcurrency != 4 {
		t.Fatalf("grafana defaults = lookback %q limit %d max failures %d concurrency %d", config.GrafanaLogLookback, config.GrafanaLogLimit, config.GrafanaLogMaxFailures, config.GrafanaLogConcurrency)
	}
}

func TestConfigUsesGrafanaReportURLFallback(t *testing.T) {
	t.Parallel()

	config := configFromEnv(map[string]string{
		"INPUT_TEST_RESULTS_PATH": "results.xml",
		"GRAFANA_REPORT_URL":      "https://nks-dev-glo1-grafana.nscale.teleport.sh",
		"GRAFANA_URL":             "http://127.0.0.1:3000",
		"GRAFANA_ORG_ID":          "7",
	})

	if config.GrafanaURL != "https://nks-dev-glo1-grafana.nscale.teleport.sh" {
		t.Fatalf("grafana URL fallback = %q", config.GrafanaURL)
	}
	if config.GrafanaOrgID != "7" {
		t.Fatalf("grafana org ID fallback = %q", config.GrafanaOrgID)
	}

	config = configFromEnv(map[string]string{
		"INPUT_TEST_RESULTS_PATH": "results.xml",
		"INPUT_GRAFANA_URL":       "https://grafana.example.com",
		"GRAFANA_REPORT_URL":      "https://nks-dev-glo1-grafana.nscale.teleport.sh",
	})
	if config.GrafanaURL != "https://grafana.example.com" {
		t.Fatalf("explicit grafana URL should win, got %q", config.GrafanaURL)
	}
}

func TestClaudePromptRequestsPatternSummary(t *testing.T) {
	t.Parallel()

	prompt := claudePrompt()
	for _, expected := range []string{
		"4-6 high-signal Slack mrkdwn bullet lines",
		"Classify each pattern as one of: infra/external, code/core logic, test/false failure, skipped, unknown/mixed",
		"Use skipped for patterns where all affected tests are skipped",
		"Use test/false failure only for failed tests",
		"If Grafana/Loki observations are present, use them only as supporting evidence",
		"Keep the report close to the existing production format",
		"do not add a separate Grafana/Loki section",
		"When a Loki signal is present, mention the concrete signal",
		"Do not overstate certainty when Loki returned empty, cleanup-only, or loosely related logs",
		`add a "### Representative Failed Tests" table capped at 10 rows`,
		"group tests with the same failure reason into one row",
		"Each pattern bullet must start with '- *<suite/category>* (<category>):'",
		"Each pattern bullet must answer: which suite/test area failed, what failed, and the likely reason",
		"For Grafana/Loki-backed bullets, explicitly connect the test error",
		`Do not use vague phrases like "Grafana returned related activity"`,
		"If Loki only returned audit/cleanup rows",
		"Group by suite name when one suite is affected",
		"Lead with the highest-attention real product, infra, or environment blocker",
		"keep temporary sentinel/test-validation failures short",
		"Include only the evidence needed to justify the category",
		"avoid selector names, file paths, and retry details",
		"Use at most one supporting bullet such as '- *Evidence:*' or '- *Impact:*'",
		"For intentional or sentinel skipped tests",
		"re-enabled",
		"For intentional or sentinel failed tests",
		"removed or disabled before review",
		"Do not list every failed or skipped test",
		"End with exactly one '- *Action:*' bullet",
		"the Action bullet must mention that test-level failure reasons are available in the GitHub build summary",
		"Do not mention test-level failure reasons for skip-only runs",
		"- *Auth / all suites* (infra/external):",
		"| Suite / area | Representative tests | Failure reason | Count |",
		"- *Impact:* Multiple setup-dependent suites are blocked before product-level assertions run.",
		"- *File Storage input validation* (skipped): 1 test is intentionally skipped for known bug INST-457",
		"- *File Storage attachment network* (infra/external): The test failed because network provisioning reached error instead of provisioned; Loki matched the resource only in audit/cleanup rows",
		"- *Action:* Use the GitHub build summary for test-level failure reasons;",
		aiSlackDelimiter,
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "- *Details:* Test-level failure reasons are available in the GitHub build summary.") {
		t.Fatalf("prompt should not include a separate Details bullet:\n%s", prompt)
	}
}

func TestGrafanaLogQueryPlanningPromptRequestsBackendOnlyJSON(t *testing.T) {
	t.Parallel()

	prompt := grafanaLogQueryPlanningPrompt()
	for _, expected := range []string{
		"Return strict JSON only",
		`"test_name"`,
		`"backend_area"`,
		`"expected_error"`,
		`"search_terms"`,
		`"confidence"`,
		"Only create queries for failures that appear backend-related",
		"Do not query for purely client-side assertion failures",
		"Use the exact failure_ref values",
		"Do not include Grafana URLs",
		"do not assume a single backend component",
		`return {"queries":[]}`,
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("planning prompt missing %q:\n%s", expected, prompt)
		}
	}
}

func TestRenderGrafanaLogQueryPlanningInputIncludesFailureRefs(t *testing.T) {
	t.Parallel()

	input := renderGrafanaLogQueryPlanningInput(Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Passed: 1, Failed: 2},
		Failures: []TestCase{{
			ID:      "file-upload",
			Name:    "uploads file",
			Suite:   "File Storage Management",
			File:    "packages/e2e-console/src/spec/file-storage.spec.ts",
			Message: "POST /api/storage returned 500 for claim-123",
		}, {
			ID:      "visual-only",
			Name:    "button color",
			Suite:   "Visual checks",
			Message: "expected CSS color to match",
		}},
	}, Config{
		Environment:           "dev",
		GrafanaLogMaxFailures: 2,
	})

	for _, expected := range []string{
		"Failure ref: f1",
		"Test ID: file-upload",
		"POST /api/storage returned 500 for claim-123",
		"Failure ref: f2",
		"Visual checks",
		"Maximum queries allowed: 2",
	} {
		if !strings.Contains(input, expected) {
			t.Fatalf("planning input missing %q:\n%s", expected, input)
		}
	}
}

func TestParseGrafanaLogQueryPlan(t *testing.T) {
	t.Parallel()

	queries, err := parseGrafanaLogQueryPlan("```json\n{\"queries\":[{\"failure_ref\":\" f1 \",\"test_name\":\" uploads file \",\"backend_area\":\" file-storage \",\"expected_error\":\" POST /api/storage returned 500 for claim-123 \",\"search_terms\":[\" claim-123 \",\"file-storage\",\"claim-123\"],\"logql\":\" {namespace=~\\\".+\\\"} |= \\\"claim-123\\\" \",\"reason\":\" storage backend error \",\"confidence\":\" Medium \"},{\"failure_ref\":\"f2\"}]}\n```")
	if err != nil {
		t.Fatalf("parseGrafanaLogQueryPlan returned error: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("queries = %+v", queries)
	}
	if queries[0].FailureRef != "f1" ||
		queries[0].TestName != "uploads file" ||
		queries[0].BackendArea != "file-storage" ||
		queries[0].ExpectedError != "POST /api/storage returned 500 for claim-123" ||
		len(queries[0].SearchTerms) != 2 ||
		queries[0].Confidence != "medium" ||
		!strings.Contains(queries[0].LogQL, "claim-123") ||
		queries[0].Reason != "storage backend error" {
		t.Fatalf("unexpected query: %+v", queries[0])
	}
}

func TestRenderAIInputIncludesGrafanaLogs(t *testing.T) {
	t.Parallel()

	input := renderAIInputWithOptions(Analysis{
		Current: TestRun{Name: "API Tests"},
		Stats:   Stats{Passed: 1, Failed: 1},
		Failures: []TestCase{{
			Name:    "creates instance",
			Message: "timeout",
		}},
		GrafanaLogs: &GrafanaLogEnrichment{
			DatasourceUID:  "loki",
			DatasourceName: "Loki",
			StartRFC3339:   "2026-06-01T13:00:00Z",
			EndRFC3339:     "2026-06-01T14:00:00Z",
			Contexts: []GrafanaLogContext{{
				FailureRef:        "f1",
				TestName:          "creates instance",
				BackendArea:       "unikorn-region",
				ExpectedError:     "instance reconcile timed out",
				SearchTerms:       []string{"instance", "timeout"},
				Query:             `{namespace="unikorn-region"} |= "instance"`,
				GrafanaExploreURL: "https://grafana.example.com/explore?panes=test",
				Reason:            "Instance API returned a backend reconcile timeout.",
				Confidence:        "high",
				LineCount:         1,
				FilteredLineCount: 2,
				Entries: []GrafanaLogEntry{{
					Timestamp: "1780322400000000000",
					Line:      "controller failed to create instance",
					Labels: map[string]string{
						"namespace": "unikorn-region",
						"pod":       "region-api-123",
					},
				}},
			}},
		},
	}, AIInputOptions{})

	for _, expected := range []string{
		"Grafana/Loki observations for final analysis:",
		"Scope: time range 2026-06-01T13:00:00Z to 2026-06-01T14:00:00Z; datasource Loki (loki).",
		"- Test: creates instance; backend: unikorn-region; confidence: high; Loki returned 1 matching log line; components: unikorn-region; first match at 2026-06-01T14:00:00Z from unikorn-region; Loki signal: error signals: failed; filtered 2 Grafana/MCP self-observability line(s); neutral Grafana link is included in the GitHub summary",
		"Lookup reason: Instance API returned a backend reconcile timeout.",
		"Failed tests:",
	} {
		if !strings.Contains(input, expected) {
			t.Fatalf("AI input missing %q:\n%s", expected, input)
		}
	}
	for _, unexpected := range []string{
		`Query: {namespace="unikorn-region"} |= "instance"`,
		"Exact failure error: instance reconcile timed out",
		"Search terms: instance, timeout",
		"Grafana lookup URL: https://grafana.example.com/explore?panes=test",
		"controller failed to create instance",
	} {
		if strings.Contains(input, unexpected) {
			t.Fatalf("AI input should not include %q:\n%s", unexpected, input)
		}
	}
}

func TestGrafanaLogSignalSummaryIncludesCleanupOnlyObservation(t *testing.T) {
	t.Parallel()

	summary := grafanaLogSignalSummary(GrafanaLogContext{
		Entries: []GrafanaLogEntry{{
			Line: `{"level":"info","msg":"audit","spanName":"/api/v2/networks/network-123"`,
		}, {
			Line: `{"level":"info","msg":"deletion complete","controller":"unikorn-network-controller"}`,
		}},
	})

	if summary != "no explicit error string in returned rows" {
		t.Fatalf("signal summary should not include raw Loki messages: %s", summary)
	}
	for _, unexpected := range []string{
		"audit",
		"deletion complete",
		"matched messages",
	} {
		if strings.Contains(summary, unexpected) {
			t.Fatalf("signal summary should not include raw message %q: %s", unexpected, summary)
		}
	}
}

func TestGrafanaLogSignalSummaryIncludesErrorObservation(t *testing.T) {
	t.Parallel()

	summary := grafanaLogSignalSummary(GrafanaLogContext{
		Entries: []GrafanaLogEntry{{
			Line: "ERROR: Failed to forward scheduled tasks: INTERNAL_ERROR: redis eval error: connect: connection refused",
		}},
	})

	for _, expected := range []string{
		"error signals: INTERNAL_ERROR",
		"connection refused",
		"failed",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("signal summary missing %q: %s", expected, summary)
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
