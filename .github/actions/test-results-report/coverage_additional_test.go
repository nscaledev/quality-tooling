package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeCommandHelpersUseClaudeCodeOutput(t *testing.T) {
	dir := t.TempDir()
	stdinPath := filepath.Join(dir, "stdin.txt")
	npxPath := filepath.Join(dir, "npx")
	script := `#!/bin/sh
if [ -n "${FAKE_CLAUDE_STDIN:-}" ]; then
  cat > "${FAKE_CLAUDE_STDIN}"
else
  cat >/dev/null
fi
case "${FAKE_CLAUDE_MODE}" in
  analysis)
    printf '## Test Failure Analysis\n\nAI connected test failure to Loki evidence\n%s\n- *Action:* Use the GitHub build summary for test-level failure reasons.\n' "${AI_SLACK_DELIMITER}"
    ;;
  plan)
    printf '{"queries":[{"failure_ref":"f1","test_name":"uploads file","backend_area":"file-storage","expected_error":"POST /storage returned 500","search_terms":["claim-123","500"],"logql":"file-storage claim-123","reason":"Backend 500 needs Loki evidence.","confidence":"high"}]}'
    ;;
  error)
    echo "fake claude failed" >&2
    exit 7
    ;;
esac
`
	if err := os.WriteFile(npxPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CLAUDE_STDIN", stdinPath)
	t.Setenv("AI_SLACK_DELIMITER", aiSlackDelimiter)
	t.Setenv("FAKE_CLAUDE_MODE", "analysis")

	analysis := Analysis{
		Current: TestRun{Name: "Console E2E"},
		Stats:   Stats{Failed: 1, Total: 1},
		Failures: []TestCase{{
			Name:    "uploads file",
			Message: "POST /storage returned 500",
		}},
	}
	result, err := runClaudeAnalysis(context.Background(), Config{
		EnableAIAnalysis: true,
		ClaudeToken:      "token",
		MaxFailures:      5,
		MaxSkips:         3,
	}, analysis)
	if err != nil {
		t.Fatalf("runClaudeAnalysis returned error: %v", err)
	}
	if result == nil || !strings.Contains(result.StepSummary, "AI connected") || !strings.Contains(result.SlackSummary, "GitHub build summary") {
		t.Fatalf("unexpected AI result: %+v", result)
	}
	input := readFile(t, stdinPath)
	if !strings.Contains(input, "uploads file") || !strings.Contains(input, "POST /storage returned 500") {
		t.Fatalf("Claude input did not include failure context:\n%s", input)
	}

	t.Setenv("FAKE_CLAUDE_MODE", "plan")
	queries, err := runClaudeGrafanaLogQueryPlanning(context.Background(), Config{
		EnableAIAnalysis:      true,
		ClaudeToken:           "token",
		GrafanaLogMaxFailures: 2,
	}, analysis)
	if err != nil {
		t.Fatalf("runClaudeGrafanaLogQueryPlanning returned error: %v", err)
	}
	if len(queries) != 1 || queries[0].FailureRef != "f1" || queries[0].Confidence != "high" || !strings.Contains(queries[0].LogQL, "claim-123") {
		t.Fatalf("unexpected planned queries: %+v", queries)
	}
	if queries, err := runClaudeGrafanaLogQueryPlanning(context.Background(), Config{EnableAIAnalysis: false, ClaudeToken: "token"}, analysis); err != nil || queries != nil {
		t.Fatalf("disabled Grafana planning should return nil result and nil error, got queries=%+v err=%v", queries, err)
	}
	if queries, err := runClaudeGrafanaLogQueryPlanning(context.Background(), Config{EnableAIAnalysis: true, ClaudeToken: "token"}, Analysis{}); err != nil || queries != nil {
		t.Fatalf("empty Grafana planning input should return nil result and nil error, got queries=%+v err=%v", queries, err)
	}
	if queries, err := runClaudeGrafanaLogQueryPlanning(context.Background(), Config{EnableAIAnalysis: true}, analysis); err != nil || queries != nil {
		t.Fatalf("missing Claude token should skip Grafana planning, got queries=%+v err=%v", queries, err)
	}

	t.Setenv("FAKE_CLAUDE_MODE", "error")
	if _, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: true, ClaudeToken: "token"}, analysis); err == nil || !strings.Contains(err.Error(), "fake claude failed") {
		t.Fatalf("expected fake Claude error, got %v", err)
	}
	if _, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: true}, analysis); err == nil || !strings.Contains(err.Error(), "claude-token") {
		t.Fatalf("expected missing token error, got %v", err)
	}
	if result, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: false}, analysis); err != nil || result != nil {
		t.Fatalf("disabled AI should return nil result and nil error, got result=%+v err=%v", result, err)
	}
	if result, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: true, ClaudeToken: "token"}, Analysis{}); err != nil || result != nil {
		t.Fatalf("empty analysis should skip AI, got result=%+v err=%v", result, err)
	}
}

func TestClaudeGrafanaLogQueryPlanningTimeoutIsCapped(t *testing.T) {
	previousTimeout := grafanaLogQueryPlanningTimeout
	grafanaLogQueryPlanningTimeout = 20 * time.Millisecond
	defer func() {
		grafanaLogQueryPlanningTimeout = previousTimeout
	}()

	dir := t.TempDir()
	npxPath := filepath.Join(dir, "npx")
	script := `#!/bin/sh
cat >/dev/null
sleep 5
printf '{"queries":[]}'
`
	if err := os.WriteFile(npxPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	analysis := Analysis{
		Stats: Stats{Failed: 1, Total: 1},
		Failures: []TestCase{{
			Name:    "creates network",
			Message: "network reached error instead of provisioned",
		}},
	}
	started := time.Now()
	_, err := runClaudeGrafanaLogQueryPlanning(context.Background(), Config{
		EnableAIAnalysis:      true,
		ClaudeToken:           "token",
		GrafanaLogMaxFailures: 1,
	}, analysis)
	if err == nil {
		t.Fatal("expected Grafana query planning timeout error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Grafana query planning timeout took too long: %s", elapsed)
	}
	if !strings.Contains(err.Error(), "run claude grafana log query planning") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}

func TestAIPlanningInputAndExtractionBranches(t *testing.T) {
	t.Parallel()

	networkFailure := TestCase{
		ID:      "network-create",
		Name:    "creates network",
		Suite:   "Network Management",
		File:    "test/api/network_test.go",
		Line:    42,
		Message: "network id 4c5a7f2e1a2b3c4d reached provisioningStatus error",
		Output:  "traceID=dca608552b223f324647576f1fcd40b7",
	}
	analysis := Analysis{
		Current: TestRun{Name: "Region API"},
		Stats:   Stats{Failed: 2, Skipped: 1, Total: 3},
		Failures: []TestCase{networkFailure, {
			ID:      "visual",
			Name:    "button has primary color",
			Message: "expected css color",
		}},
		Compare: &Comparison{
			NewFailures:       []TestCase{networkFailure},
			RecurringFailures: []TestCase{{ID: "visual", Name: "button has primary color"}},
			ResolvedFailures:  []TestCase{{ID: "old", Name: "old failure"}},
		},
	}
	input := renderGrafanaLogQueryPlanningInput(analysis, Config{
		Environment:           "dev",
		GrafanaLogMaxFailures: 2,
	})
	for _, expected := range []string{
		"Previous result comparison:",
		"New failures: 1",
		"Failure ref: f1",
		"Location: test/api/network_test.go:42",
		"Failure keyword regex:",
		"dca608552b223f324647576f1fcd40b7",
	} {
		if !strings.Contains(input, expected) {
			t.Fatalf("planning input missing %q:\n%s", expected, input)
		}
	}
	if grafanaLogQueryPlanningTimeout != 90*time.Second {
		t.Fatalf("grafana query planning timeout = %s, want 1m30s", grafanaLogQueryPlanningTimeout)
	}
	planningPrompt := grafanaLogQueryPlanningPrompt()
	for _, expected := range []string{
		"provisioningStatus mismatches",
		"Resource UUIDs",
		"Cloud resource identifiers",
	} {
		if !strings.Contains(planningPrompt, expected) {
			t.Fatalf("planning prompt missing backend signal %q:\n%s", expected, planningPrompt)
		}
	}

	for _, tc := range []struct {
		output string
		want   string
	}{
		{`{"queries":[]}`, `{"queries":[]}`},
		{"```json\n{\"queries\":[]}\n```", `{"queries":[]}`},
		{"prefix text {\"queries\":[{\"failure_ref\":\"f1\",\"logql\":\"q\"}]} suffix", `{"queries":[{"failure_ref":"f1","logql":"q"}]}`},
		{"no json here", "no json here"},
	} {
		if got := extractJSONObject(tc.output); got != tc.want {
			t.Fatalf("extractJSONObject(%q) = %q, want %q", tc.output, got, tc.want)
		}
	}
}

func TestConfigParsingCoversOverridesAndEnvironment(t *testing.T) {
	env := map[string]string{
		"INPUT_TEST_RESULTS_PATH":             "results.xml",
		"INPUT_FORMAT":                        "junit",
		"INPUT_PREVIOUS_RESULTS_PATH":         "previous.xml",
		"INPUT_PREVIOUS_RESULTS_FORMAT":       "",
		"INPUT_COMPARE_WITH_PREVIOUS":         "auto",
		"INPUT_WRITE_STEP_SUMMARY":            "off",
		"INPUT_SEND_SLACK":                    "auto",
		"INPUT_SLACK_WEBHOOK_URL":             "https://hooks.slack.test",
		"INPUT_FAIL_ON_SLACK_ERROR":           "yes",
		"INPUT_TITLE":                         "Region API",
		"INPUT_ENVIRONMENT":                   "uat",
		"INPUT_DEPLOYED_VERSION":              "v1.18.0-rc1",
		"GITHUB_REF_NAME":                     "feature/test",
		"GITHUB_ACTOR":                        "octocat",
		"GITHUB_SERVER_URL":                   "https://github.example",
		"GITHUB_REPOSITORY":                   "nscale/repo",
		"GITHUB_RUN_ID":                       "12345",
		"INPUT_REPORT_URL":                    "https://reports.example/allure",
		"INPUT_PUBLISH_TEST_HISTORY":          "auto",
		"TEST_HISTORY_API_URL":                "https://history.example",
		"TEST_HISTORY_TOKEN":                  "env-history-token",
		"INPUT_TEST_HISTORY_SUITE":            "region-api",
		"INPUT_TEST_HISTORY_FRAMEWORK":        "ginkgo",
		"INPUT_TEST_HISTORY_ENV":              "qa",
		"INPUT_TEST_HISTORY_REPO":             "nscale/override",
		"INPUT_TEST_HISTORY_BRANCH":           "history-branch",
		"INPUT_TEST_HISTORY_COMMIT":           "commit-abc",
		"INPUT_TEST_HISTORY_RUN_ID":           "run-99",
		"INPUT_TEST_HISTORY_RUN_ATTEMPT":      "3",
		"INPUT_TEST_HISTORY_ARTIFACT_URL":     "https://artifacts.example/run-99",
		"GITHUB_WORKSPACE":                    "/workspace/repo",
		"INPUT_MAX_FAILURES":                  "12",
		"INPUT_MAX_SKIPS":                     "13",
		"INPUT_INCLUDE_SKIPS":                 "no",
		"INPUT_ENABLE_AI_ANALYSIS":            "true",
		"INPUT_AI_ANALYSIS_TIMEOUT_SECONDS":   "240",
		"CLAUDE_CODE_OAUTH_TOKEN":             "env-token",
		"INPUT_ENABLE_GRAFANA_LOG_ENRICHMENT": "on",
		"INPUT_GRAFANA_URL":                   "https://grafana.example.com",
		"INPUT_GRAFANA_ORG_ID":                "9",
		"INPUT_GRAFANA_MCP_ENDPOINT":          "http://127.0.0.1:8000/mcp",
		"INPUT_GRAFANA_LOKI_DATASOURCE_UID":   "loki-dev",
		"INPUT_GRAFANA_LOKI_DATASOURCE_NAME":  "Prod Loki",
		"INPUT_GRAFANA_LOG_START":             "2026-06-01T13:00:00Z",
		"INPUT_GRAFANA_LOG_END":               "2026-06-01T14:00:00Z",
		"INPUT_GRAFANA_LOG_LOOKBACK":          "3h",
		"INPUT_GRAFANA_LOG_LIMIT":             "25",
		"INPUT_GRAFANA_LOG_MAX_FAILURES":      "7",
		"INPUT_GRAFANA_LOG_CONCURRENCY":       "8",
		"INPUT_ENABLE_UNIKORN_CR_ENRICHMENT":  "true",
		"INPUT_UNIKORN_CR_PLAN_PATH":          "/tmp/unikorn-cr-plan.json",
		"INPUT_UNIKORN_CR_CONTEXT_PATH":       "/tmp/unikorn-cr-context.json",
		"INPUT_UNIKORN_CR_MAX_FAILURES":       "5",
		"INPUT_UNIKORN_CR_TIMEOUT_SECONDS":    "12",
	}
	config := configFromEnv(env)

	if config.Format != "junit" || !config.CompareWithPrevious || config.PreviousResultsFormat != "junit" {
		t.Fatalf("unexpected format/compare config: %+v", config)
	}
	if config.WriteStepSummary || !config.SendSlack || !config.FailOnSlackError || config.IncludeSkips {
		t.Fatalf("unexpected boolean config: %+v", config)
	}
	if config.DeployedVersion != "v1.18.0-rc1" || config.Branch != "feature/test" || config.Actor != "octocat" || config.WorkflowURL != "https://github.example/nscale/repo/actions/runs/12345" {
		t.Fatalf("unexpected GitHub defaults: %+v", config)
	}
	if config.MaxFailures != 12 || config.MaxSkips != 13 || config.GrafanaLogLimit != 25 || config.GrafanaLogMaxFailures != 7 || config.GrafanaLogConcurrency != 8 {
		t.Fatalf("unexpected numeric config: %+v", config)
	}
	if config.ClaudeToken != "env-token" || config.AIAnalysisTimeout != 240*time.Second || !config.EnableGrafanaLogs || config.GrafanaURL != "https://grafana.example.com" || config.GrafanaLokiName != "Prod Loki" {
		t.Fatalf("unexpected Grafana/AI config: %+v", config)
	}
	if !config.EnableUnikornCRs || config.UnikornCRPlanPath != "/tmp/unikorn-cr-plan.json" || config.UnikornCRContextPath != "/tmp/unikorn-cr-context.json" || config.UnikornCRMaxFailures != 5 || config.UnikornCRTimeout != 12*time.Second {
		t.Fatalf("unexpected Unikorn CR config: %+v", config)
	}
	if !config.PublishTestHistory || config.TestHistoryPublishMode != "api" || config.TestHistoryAPIURL != "https://history.example" || config.TestHistoryToken != "env-history-token" {
		t.Fatalf("unexpected test history enable/API config: %+v", config)
	}
	if config.TestHistorySuite != "region-api" || config.TestHistoryFramework != "ginkgo" || config.TestHistoryEnv != "qa" {
		t.Fatalf("unexpected test history context config: %+v", config)
	}
	if config.TestHistoryRepo != "nscale/override" || config.TestHistoryBranch != "history-branch" || config.TestHistoryCommit != "commit-abc" || config.TestHistoryRunID != "run-99" || config.TestHistoryRunAttempt != 3 {
		t.Fatalf("unexpected test history run identity config: %+v", config)
	}
	if config.TestHistoryArtifactURL != "https://artifacts.example/run-99" || config.TestHistoryOutputPath != "/workspace/repo/.test-history/events.ndjson" {
		t.Fatalf("unexpected test history artifact/spool config: %+v", config)
	}

	for _, tc := range []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"1", false, true},
		{"Y", false, true},
		{"false", true, false},
		{"n", true, false},
		{"unexpected", true, true},
		{"", false, false},
	} {
		if got := parseBoolDefault(tc.value, tc.fallback); got != tc.want {
			t.Fatalf("parseBoolDefault(%q, %t) = %t, want %t", tc.value, tc.fallback, got, tc.want)
		}
	}
	if !parseAutoBool("auto", true) || parseAutoBool("auto", false) || !parseAutoBool("yes", false) || parseAutoBool("no", true) {
		t.Fatal("parseAutoBool did not return expected values")
	}
	for _, tc := range []struct {
		name           string
		mode           string
		publishSetting string
		apiURL         string
		otlpEndpoint   string
		want           string
	}{
		{"explicit API", "api", "true", "", "", "api"},
		{"explicit OTLP", "otlp", "auto", "https://history.example", "", "otlp"},
		{"explicit publish defaults OTLP", "auto", "true", "https://history.example", "", "otlp"},
		{"legacy API auto", "auto", "auto", "https://history.example", "", "api"},
		{"OTLP endpoint auto", "auto", "auto", "", "http://127.0.0.1:14318/v1/logs", "otlp"},
		{"disabled no endpoint still resolves OTLP", "auto", "false", "", "", "otlp"},
	} {
		if got := resolveTestHistoryPublishMode(tc.mode, tc.publishSetting, tc.apiURL, tc.otlpEndpoint); got != tc.want {
			t.Fatalf("%s: resolveTestHistoryPublishMode() = %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value    string
		fallback int
		want     int
	}{
		{"42", 7, 42},
		{"0", 7, 7},
		{"-1", 7, 7},
		{"bad", 7, 7},
		{"", 7, 7},
	} {
		if got := parseIntDefault(tc.value, tc.fallback); got != tc.want {
			t.Fatalf("parseIntDefault(%q, %d) = %d, want %d", tc.value, tc.fallback, got, tc.want)
		}
	}
}

func TestLoadConfigUsesProcessEnvironment(t *testing.T) {
	t.Setenv("INPUT_TEST_RESULTS_PATH", "results.xml")
	t.Setenv("INPUT_SEND_SLACK", "true")
	t.Setenv("INPUT_SLACK_WEBHOOK_URL", "https://hooks.slack.test")

	config := loadConfig()

	if config.TestResultsPath != "results.xml" || !config.SendSlack || config.SlackWebhookURL == "" {
		t.Fatalf("loadConfig did not read process environment: %+v", config)
	}
}

func TestMainHelpersCoverOutputAndPathBranches(t *testing.T) {
	dir := t.TempDir()
	summaryPath := filepath.Join(dir, "summary.md")
	if err := appendStepSummary(summaryPath, "first\n"); err != nil {
		t.Fatalf("append summary first: %v", err)
	}
	if err := appendStepSummary(summaryPath, "second\n"); err != nil {
		t.Fatalf("append summary second: %v", err)
	}
	if content := readFile(t, summaryPath); content != "first\nsecond\n" {
		t.Fatalf("summary content = %q", content)
	}
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	err = appendStepSummary("", "printed summary\n")
	os.Stdout = oldStdout
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("close stdout pipe writer: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("append empty summary path: %v", err)
	}
	printed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if string(printed) != "printed summary\n" {
		t.Fatalf("captured stdout = %q", printed)
	}

	outputPath := filepath.Join(dir, "outputs.txt")
	analysis := Analysis{
		Current: TestRun{Duration: 1500 * time.Millisecond},
		Stats:   Stats{Total: 3, Passed: 1, Failed: 1, Skipped: 1},
		Compare: &Comparison{
			NewFailures:       []TestCase{{Name: "new"}},
			RecurringFailures: []TestCase{{Name: "recurring"}},
			ResolvedFailures:  []TestCase{{Name: "resolved"}},
			NewSkips:          []TestCase{{Name: "new skip"}},
			RecurringSkips:    []TestCase{{Name: "recurring skip"}},
			ResolvedSkips:     []TestCase{{Name: "resolved skip"}},
		},
	}
	if err := writeOutputs(outputPath, analysis, true); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
	outputs := readFile(t, outputPath)
	for _, expected := range []string{
		"total=3",
		"failed=1",
		"duration=1.5s",
		"duration-ms=1500",
		"conclusion=failure",
		"new-failures=1",
		"slack-sent=true",
	} {
		if !strings.Contains(outputs, expected) {
			t.Fatalf("outputs missing %q:\n%s", expected, outputs)
		}
	}
	if err := writeOutputs("", analysis, false); err != nil {
		t.Fatalf("empty output path should be ignored: %v", err)
	}
	if conclusion(Analysis{Stats: Stats{Failed: 0}}) != "success" || conclusion(analysis) != "failure" {
		t.Fatalf("unexpected conclusion values")
	}

	if _, err := readAndParse(filepath.Join(dir, "missing.xml"), "junit"); err == nil || !strings.Contains(err.Error(), "stat test results path") {
		t.Fatalf("expected readAndParse stat error, got %v", err)
	}
	emptyDir := filepath.Join(dir, "empty")
	if err := os.Mkdir(emptyDir, 0o700); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if _, err := resolveResultsPath(emptyDir, "auto"); err == nil || !strings.Contains(err.Error(), "no supported test result files") {
		t.Fatalf("expected no supported files error, got %v", err)
	}
}

func TestMCPProtocolAndToolEdgeCases(t *testing.T) {
	t.Parallel()

	message, err := decodeMCPResponseBody("text/event-stream", []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	if err != nil {
		t.Fatalf("decode SSE MCP response: %v", err)
	}
	if !strings.Contains(string(message.Result), "ok") {
		t.Fatalf("unexpected SSE result: %s", message.Result)
	}
	if _, err := firstSSEDataPayload("event: ping\ndata: [DONE]\n\n"); err == nil || !strings.Contains(err.Error(), "no JSON data payload") {
		t.Fatalf("expected missing JSON SSE error, got %v", err)
	}
	if _, err := decodeMCPResponseBody("application/json", []byte("{bad json")); err == nil {
		t.Fatal("expected invalid JSON decode error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "session-1")
		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{"protocolVersion": mcpProtocolVersion})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusNoContent)
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode tool params: %v", err)
			}
			switch params.Name {
			case "empty":
				writeMCPResponse(t, writer, rpc.ID, map[string]any{"content": []map[string]string{}})
			case "codefence":
				writeMCPToolResponse(t, writer, rpc.ID, "```json\n{\"ok\":true}\n```")
			case "error":
				writeMCPResponse(t, writer, rpc.ID, map[string]any{
					"content": []map[string]string{{"type": "text", "text": "tool failed"}},
					"isError": true,
				})
			default:
				t.Fatalf("unexpected tool %s", params.Name)
			}
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(server.URL)
	raw, err := client.callTool(context.Background(), "empty", nil)
	if err != nil {
		t.Fatalf("empty tool returned error: %v", err)
	}
	if !strings.Contains(string(raw), "content") {
		t.Fatalf("empty text tool should return raw payload, got %s", raw)
	}
	raw, err = client.callTool(context.Background(), "codefence", nil)
	if err != nil {
		t.Fatalf("codefence tool returned error: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("codefence JSON was not extracted: %s", raw)
	}
	if _, err := client.callTool(context.Background(), "error", nil); err == nil || !strings.Contains(err.Error(), "tool failed") {
		t.Fatalf("expected tool error, got %v", err)
	}
}

func TestMCPPostAndDatasourceErrorBranches(t *testing.T) {
	t.Parallel()

	statusServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "bad gateway", http.StatusBadGateway)
	}))
	defer statusServer.Close()
	statusClient := newMCPHTTPClient(statusServer.URL)
	if _, err := statusClient.request(context.Background(), "initialize", map[string]any{}); err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected status error, got %v", err)
	}

	emptyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
	}))
	defer emptyServer.Close()
	emptyClient := newMCPHTTPClient(emptyServer.URL)
	if _, err := emptyClient.request(context.Background(), "initialize", map[string]any{}); err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("expected empty response error, got %v", err)
	}

	uid, name, err := resolveLokiDatasource(context.Background(), nil, Config{GrafanaLokiUID: "loki-fixed", GrafanaLokiName: "Fixed Loki"})
	if err != nil || uid != "loki-fixed" || name != "Fixed Loki" {
		t.Fatalf("caller-provided datasource not returned: uid=%q name=%q err=%v", uid, name, err)
	}

	datasourceServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{"protocolVersion": mcpProtocolVersion})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[{"uid":"loki-a","name":"Loki A","type":"loki"},{"uid":"loki-prod","name":"Prod Loki","type":"loki","isDefault":true}]}`)
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
		}
	}))
	defer datasourceServer.Close()
	uid, name, err = resolveLokiDatasource(context.Background(), newMCPHTTPClient(datasourceServer.URL), Config{GrafanaLokiName: "Prod Loki"})
	if err != nil || uid != "loki-prod" || name != "Prod Loki" {
		t.Fatalf("named datasource not selected: uid=%q name=%q err=%v", uid, name, err)
	}

	noDatasourceServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{"protocolVersion": mcpProtocolVersion})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[]}`)
		}
	}))
	defer noDatasourceServer.Close()
	if _, _, err := resolveLokiDatasource(context.Background(), newMCPHTTPClient(noDatasourceServer.URL), Config{}); err == nil || !strings.Contains(err.Error(), "no Loki datasource") {
		t.Fatalf("expected no datasource error, got %v", err)
	}
}

func TestGrafanaSelectionTimeRangeAndQueryErrorBranches(t *testing.T) {
	t.Parallel()

	analysis := Analysis{
		Failures: []TestCase{
			{ID: "old-failure", Name: "old"},
			{ID: "new-failure", Name: "new"},
			{ID: "backfill", Name: "backfill"},
		},
		Compare: &Comparison{
			NewFailures: []TestCase{{ID: "new-failure", Name: "new"}},
		},
	}
	selected := selectFailuresForGrafanaLogs(analysis, 2)
	if len(selected) != 2 || selected[0].ID != "new-failure" || selected[1].ID != "old-failure" {
		t.Fatalf("unexpected selected failures: %+v", selected)
	}
	if len(limitGrafanaPlannedQueries([]GrafanaLogPlannedQuery{{FailureRef: "f1"}, {FailureRef: "f2"}}, 1)) != 1 {
		t.Fatal("planned query limit was not applied")
	}
	if normalizedGrafanaLogConcurrency(0, 10) != 4 || normalizedGrafanaLogConcurrency(20, 2) != 2 || normalizedGrafanaLogConcurrency(0, 0) != 0 {
		t.Fatal("unexpected normalized concurrency")
	}
	if _, _, err := grafanaLogTimeRange(Config{GrafanaLogLookback: "not-a-duration"}, time.Now()); err == nil {
		t.Fatal("expected invalid lookback error")
	}
	if _, _, err := grafanaLogTimeRange(Config{GrafanaLogStart: "bad", GrafanaLogLookback: "1h"}, time.Now()); err == nil {
		t.Fatal("expected invalid start error")
	}
	if _, _, err := grafanaLogTimeRange(Config{GrafanaLogEnd: "bad"}, time.Now()); err == nil {
		t.Fatal("expected invalid end error")
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{"protocolVersion": mcpProtocolVersion})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode params: %v", err)
			}
			if params.Name != "query_loki_logs" {
				t.Fatalf("unexpected tool %s", params.Name)
			}
			writeMCPToolResponse(t, writer, rpc.ID, `not-json`)
		}
	}))
	defer errorServer.Close()

	context := queryGrafanaLogs(context.Background(), newMCPHTTPClient(errorServer.URL), "loki", `{namespace=~".+"}`, "2026-06-01T13:00:00Z", "2026-06-01T14:00:00Z", 5, nil, "AI-planned backend query", "planned lookup")
	if context.Error == "" || !strings.Contains(context.Error, "decode query_loki_logs result") {
		t.Fatalf("expected decode error context, got %+v", context)
	}
}

func TestRenderUtilityBranches(t *testing.T) {
	t.Parallel()

	if grafanaSummaryURL("") != "" || grafanaSummaryURL("not a url") != "" {
		t.Fatal("invalid Grafana summary URLs should be omitted")
	}
	if got := grafanaSummaryURL("https://grafana.example.com/grafana/explore?panes=secret#frag"); got != "https://grafana.example.com/grafana/explore?panes=secret" {
		t.Fatalf("summary URL = %q", got)
	}
	intro := grafanaObservationIntro(&GrafanaLogEnrichment{})
	if !strings.Contains(intro, "query details are omitted") {
		t.Fatalf("unexpected empty Grafana intro: %s", intro)
	}
	observation := grafanaObservationText(GrafanaLogContext{LineCount: 2, Truncated: true})
	if !strings.Contains(observation, "2 matching log lines") || !strings.Contains(observation, "results truncated") {
		t.Fatalf("unexpected observation: %s", observation)
	}
	if grafanaLogComponentSummary([]GrafanaLogEntry{{Labels: map[string]string{"pod": "pod-a"}}, {Labels: map[string]string{"namespace": "ns"}}, {Labels: map[string]string{"container": "container"}}, {Labels: map[string]string{"app": "extra"}}}) != "pod-a, ns, container" {
		t.Fatal("component summary should cap at three unique components")
	}
	if formatDuration(500*time.Millisecond) != "500ms" || formatDuration(1500*time.Millisecond) != "1.5s" || formatDuration(2*time.Minute) != "2.0m" {
		t.Fatal("formatDuration did not cover expected branches")
	}
	if formatSignedDuration(-1500*time.Millisecond) != "-1.5s" || formatSignedDuration(0) != "0s" {
		t.Fatal("formatSignedDuration did not cover expected branches")
	}
	if truncate("short", 10) != "short" || truncate("abcdef", 4) != "a..." || truncate("abcdef", 0) != "abcdef" {
		t.Fatal("truncate did not cover expected branches")
	}
	if formatLogTimestamp("2026-06-01T14:00:00Z") != "2026-06-01T14:00:00Z" ||
		formatLogTimestamp("1780322400000000000") != "2026-06-01T14:00:00Z" ||
		formatLogTimestamp(`"1780322400000000000"`) != "2026-06-01T14:00:00Z" ||
		formatLogTimestamp("bad") != "bad" ||
		formatLogTimestamp("") != "-" {
		t.Fatal("formatLogTimestamp did not cover expected branches")
	}
	labels := formatLogLabels(map[string]string{"namespace": "unikorn-region", "pod": "pod-a"})
	if !strings.Contains(labels, "namespace=unikorn-region") || !strings.Contains(labels, "pod=pod-a") || formatLogLabels(nil) != "-" {
		t.Fatalf("unexpected label formatting: %q", labels)
	}
}

func TestParseAndFormatEdgeCases(t *testing.T) {
	t.Parallel()

	if _, err := parseTestResults([]byte(`{"unknown":true}`), "unknown-format"); err == nil {
		t.Fatal("expected unsupported format error")
	}
	if _, err := detectFormat([]byte(`not a report`)); err == nil {
		t.Fatal("expected detect format error")
	}
	if normalizeFormat("xml") != formatJUnit || normalizeFormat("playwright") != formatPlaywrightJSON || normalizeFormat("ginkgo") != formatGinkgoJSON || normalizeFormat("other") != "other" {
		t.Fatal("normalizeFormat did not return expected aliases")
	}
	if parseSecondsDuration("bad") != 0 || parseSecondsDuration("") != 0 || parseSecondsDuration("1.25") != 1250*time.Millisecond {
		t.Fatal("parseSecondsDuration edge cases failed")
	}
	if normalizeStatus("passed") != StatusPassed || normalizeStatus("skipped") != StatusSkipped || normalizeStatus("failed") != StatusFailed || normalizeStatus("weird") != StatusOther {
		t.Fatal("normalizeStatus edge cases failed")
	}
	if firstFile([]string{"", "src/a.go"}) != "src/a.go" || firstString([]string{"", "hello"}) != "hello" {
		t.Fatal("simple parser helpers failed")
	}
	if cleaned := nonEmpty([]string{"", " x ", "y"}); len(cleaned) != 2 || cleaned[0] != "x" || cleaned[1] != "y" {
		t.Fatalf("nonEmpty did not trim/filter values: %+v", cleaned)
	}
	if stableID("suite", "", "test") != "suite::test" {
		t.Fatal("stableID did not filter empty parts")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
