package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type AIAnalysis struct {
	StepSummary  string
	SlackSummary string
}

type GrafanaLogPlannedQuery struct {
	FailureRef    string   `json:"failure_ref"`
	TestName      string   `json:"test_name,omitempty"`
	BackendArea   string   `json:"backend_area,omitempty"`
	ExpectedError string   `json:"expected_error,omitempty"`
	SearchTerms   []string `json:"search_terms,omitempty"`
	LogQL         string   `json:"logql"`
	Reason        string   `json:"reason,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
}

type grafanaLogQueryPlanResponse struct {
	Queries []GrafanaLogPlannedQuery `json:"queries"`
}

type AIInputOptions struct {
	MaxFailures int
	MaxSkips    int
}

const aiSlackDelimiter = "<<<TEST_RESULTS_REPORT_SLACK_SUMMARY_8E5B7AE7>>>"

var runGrafanaLogQueryPlanning = runClaudeGrafanaLogQueryPlanning

func runClaudeAnalysis(ctx context.Context, config Config, analysis Analysis) (*AIAnalysis, error) {
	if !config.EnableAIAnalysis {
		return nil, nil
	}
	if len(analysis.Failures) == 0 && len(analysis.Skipped) == 0 {
		return nil, nil
	}
	if config.ClaudeToken == "" {
		return nil, fmt.Errorf("enable-ai-analysis is true but claude-token/CLAUDE_CODE_OAUTH_TOKEN is not set")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "npx", "--yes", "@anthropic-ai/claude-code", "-p", claudePrompt())
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+config.ClaudeToken)
	cmd.Stdin = strings.NewReader(renderAIInputWithOptions(analysis, AIInputOptions{
		MaxFailures: config.MaxFailures,
		MaxSkips:    config.MaxSkips,
	}))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run claude analysis: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return parseAIAnalysis(stdout.String()), nil
}

func claudePrompt() string {
	return fmt.Sprintf(`Analyze these test failures and skips. The GitHub step summary already includes run totals, links, and any previous-result comparison before your output, so do not repeat those basics, do not add separate "Failed Tests" or "Skipped Tests" sections, and do not list every test.

Output exactly two sections separated by a line containing only %q. Do not write this delimiter anywhere else.

Section 1: Markdown for the GitHub step summary.
- Start with '## Test Failure Analysis'.
- Keep it concise: one compact pattern table plus up to 4 bullets.
- Group failures and skips by likely area or pattern, not by individual test.
- Classify each pattern as one of: infra/external, code/core logic, test/false failure, skipped, unknown/mixed.
- Use skipped for patterns where all affected tests are skipped, including known-bug, intentional, disabled, pending, or sentinel skips.
- Use test/false failure only for failed tests caused by test code, invalid assertions, sentinel failures, or false failures; do not use it for skipped tests.
- Use unknown/mixed when there is not enough evidence to choose a category confidently.
- Mention representative tests only when they clarify a pattern; cap examples to 2 per row.
- If Grafana/Loki observations are present, use them only as supporting evidence inside the existing pattern rows or next-check bullets.
- Keep the report close to the existing production format; do not add a separate Grafana/Loki section, raw log table, LogQL, search terms, or Grafana URL list.
- When a Loki signal is present, mention the concrete signal in the Likely reason or Next check, such as "Loki showed INTERNAL_ERROR/connection refused" or "Loki only returned audit/cleanup rows and no explicit error".
- Do not overstate certainty when Loki returned empty, cleanup-only, or loosely related logs.
- The pattern table must make clear what failed, why it failed, the likely reason, impact, and the next check.
- When test-level detail is useful, add a "### Representative Failed Tests" table capped at 10 rows.
- In the representative tests table, group tests with the same failure reason into one row instead of listing duplicate failures separately.

Use this shape:
## Test Failure Analysis

### Patterns
| Category | What failed | Why it failed | Likely reason | Impact | Next check |
| --- | --- | --- | --- | ---: | --- |
| infra/external | Auth-dependent setup across suites | API calls returned 401 before product assertions | Expired or invalid API token | 23 failed, 37 skipped | Validate the API token, then rerun one representative suite |

### Representative Failed Tests
| Suite / area | Representative tests | Failure reason | Count |
| --- | --- | --- | ---: |
| File Storage Management | attach storage, detach storage | HTTP 401 access_denied before product assertions | 8 |

### Suggested Next Checks
- Confirm whether the failures share the same status/error before opening individual test issues.
- Rerun one representative failing suite after credentials or environment config are refreshed.

%s
Section 2: Plain text Slack summary.
- 4-6 high-signal Slack mrkdwn bullet lines.
- Do not use tables in the Slack summary; Slack should stay short bullet lines.
- Each pattern bullet must start with '- *<suite/category>* (<category>):', where category is one of infra/external, code/core logic, test/false failure, skipped, unknown/mixed.
- Each pattern bullet must answer: which suite/test area failed, what failed, and the likely reason.
- For Grafana/Loki-backed bullets, explicitly connect the test error, your interpretation, and the Loki signal in the same bullet.
- Do not use vague phrases like "Grafana returned related activity" unless you also say what Loki showed or did not show.
- If Loki only returned audit/cleanup rows, say that and point the action to the earlier provisioning/error window; if Loki returned error signals, name the signals.
- Group by suite name when one suite is affected, or by a clear category name when multiple suites share the same root cause.
- Lead with the highest-attention real product, infra, or environment blocker; keep temporary sentinel/test-validation failures short unless they are the only issue.
- Include only the evidence needed to justify the category; avoid selector names, file paths, and retry details unless they materially change the next action.
- Use at most one supporting bullet such as '- *Evidence:*' or '- *Impact:*' when it makes Slack easier to act on.
- For intentional or sentinel skipped tests, use the skipped category and one short phrase that says when the skip should be removed or re-enabled; do not mention issue alerting unless it appears in the evidence.
- For intentional or sentinel failed tests, use one short phrase that says it is temporary and should be removed or disabled before review; do not mention issue alerting unless it appears in the evidence.
- Do not list every failed or skipped test.
- Do not restate the test run title, environment, branch, actor, or full totals line; Slack already shows those fields.
- End with exactly one '- *Action:*' bullet.
- When failed tests are present, the Action bullet must mention that test-level failure reasons are available in the GitHub build summary before the next action.
- Do not mention test-level failure reasons for skip-only runs.

Use this shape:
- *Auth / all suites* (infra/external): 23 setup-dependent tests failed with HTTP 401 before product assertions; the likely reason is an expired or invalid API token.
- *Impact:* Multiple setup-dependent suites are blocked before product-level assertions run.
- *File Storage input validation* (skipped): 1 test is intentionally skipped for known bug INST-457; re-enable it once the bug is fixed.
- *File Storage attachment network* (infra/external): The test failed because network provisioning reached error instead of provisioned; Loki matched the resource only in audit/cleanup rows, so inspect the earlier controller/provisioner error window.
- *Action:* Use the GitHub build summary for test-level failure reasons; refresh the token or config, then rerun one focused smoke suite.`, aiSlackDelimiter, aiSlackDelimiter)
}

func runClaudeGrafanaLogQueryPlanning(ctx context.Context, config Config, analysis Analysis) ([]GrafanaLogPlannedQuery, error) {
	if !config.EnableAIAnalysis || config.ClaudeToken == "" {
		return nil, nil
	}
	if len(analysis.Failures) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "npx", "--yes", "@anthropic-ai/claude-code", "-p", grafanaLogQueryPlanningPrompt())
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+config.ClaudeToken)
	cmd.Stdin = strings.NewReader(renderGrafanaLogQueryPlanningInput(analysis, config))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run claude grafana log query planning: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return parseGrafanaLogQueryPlan(stdout.String())
}

func grafanaLogQueryPlanningPrompt() string {
	return `You are planning read-only Loki log queries for Grafana MCP based on parsed test failures.

Return strict JSON only. Do not include markdown, prose, or code fences.

Expected output:
{"queries":[{"failure_ref":"f1","test_name":"uploads file","backend_area":"file-storage","expected_error":"POST /api/storage returned 500 for claim-123","search_terms":["claim-123","file-storage","500"],"logql":"{namespace=~\".+\"} |~ \"(?i)(claim-123|file-storage|500)\"","reason":"The failed UI upload crossed the file storage API and includes a backend 500 signature, so Loki evidence can confirm whether file storage emitted the same error.","confidence":"medium"}]}

Rules:
- Inspect the failed test names, suites, locations, error messages, captured output, environment, and previous-result comparison.
- Only create queries for failures that appear backend-related or need backend evidence to confirm the likely cause.
- Do not query for purely client-side assertion failures when there is no backend signal.
- Use the exact failure_ref values from the input.
- test_name must match the input Test value for that failure_ref.
- expected_error must be the exact error message or shortest exact error signature from the failure evidence; leave it empty when there is no exact backend-looking error.
- search_terms must contain only identifiers, status codes, API error strings, resource names, or component names copied from the failure evidence.
- backend_area should name the likely backend component or area when the evidence supports one; otherwise use "unknown".
- reason must be one consolidated sentence explaining why this specific failure needs or does not need backend log evidence.
- confidence must be one of "high", "medium", or "low".
- Prefer precise IDs, request IDs, UUIDs, resource names, status codes, API error strings, and backend component names found in the failure evidence.
- For cross-component UI suites, do not assume a single backend component. Use a broad Kubernetes label selector such as {namespace=~".+"} unless the failure evidence clearly points to a narrower namespace or service.
- Keep each LogQL query readable and bounded for the supplied time window. Do not request writes or mutations.
- Do not include Grafana URLs in this JSON. The reporter generates grafana_explore_url deterministically after it knows the datasource, query, and time range.
- If no backend-related log lookup is justified, return {"queries":[]}.`
}

func renderGrafanaLogQueryPlanningInput(analysis Analysis, config Config) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Test run: %s\n", analysis.Current.Name))
	sb.WriteString(fmt.Sprintf("Environment: %s\n", config.Environment))
	sb.WriteString(fmt.Sprintf("Totals: %d passed, %d failed, %d skipped\n", analysis.Stats.Passed, analysis.Stats.Failed, analysis.Stats.Skipped))
	sb.WriteString(fmt.Sprintf("Maximum queries allowed: %d\n\n", normalizedGrafanaFailureLimit(config.GrafanaLogMaxFailures)))

	if analysis.Compare != nil {
		sb.WriteString("Previous result comparison:\n")
		sb.WriteString(fmt.Sprintf("New failures: %d\n", len(analysis.Compare.NewFailures)))
		sb.WriteString(fmt.Sprintf("Recurring failures: %d\n", len(analysis.Compare.RecurringFailures)))
		sb.WriteString(fmt.Sprintf("Resolved failures: %d\n", len(analysis.Compare.ResolvedFailures)))
		sb.WriteString("\n")
	}

	sb.WriteString("Candidate failed tests for backend log lookup:\n")
	for _, candidate := range selectGrafanaFailureCandidates(analysis, config.GrafanaLogMaxFailures) {
		test := candidate.Test
		sb.WriteString(fmt.Sprintf("Failure ref: %s\n", candidate.Ref))
		if test.ID != "" {
			sb.WriteString(fmt.Sprintf("Test ID: %s\n", test.ID))
		}
		sb.WriteString(fmt.Sprintf("Test: %s\n", test.Name))
		if test.Suite != "" {
			sb.WriteString(fmt.Sprintf("Suite: %s\n", test.Suite))
		}
		if location := formatLocation(test); location != "" {
			sb.WriteString(fmt.Sprintf("Location: %s\n", location))
		}
		if test.Message != "" {
			sb.WriteString(fmt.Sprintf("Error: %s\n", truncate(test.Message, 2000)))
		}
		if test.Output != "" {
			sb.WriteString(fmt.Sprintf("Output: %s\n", truncate(test.Output, 2000)))
		}
		sb.WriteString(fmt.Sprintf("Failure keyword regex: %s\n", logKeywordRegex(test)))
		sb.WriteString("\n")
	}

	if query := strings.TrimSpace(config.GrafanaLogQL); query != "" {
		sb.WriteString("Caller-provided general LogQL fallback:\n")
		sb.WriteString(query)
		sb.WriteString("\n\n")
	}
	if template := strings.TrimSpace(config.GrafanaLogQLTemplate); template != "" {
		sb.WriteString("Caller-provided per-failure LogQL template fallback:\n")
		sb.WriteString(template)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func parseGrafanaLogQueryPlan(output string) ([]GrafanaLogPlannedQuery, error) {
	var response grafanaLogQueryPlanResponse
	if err := json.Unmarshal([]byte(extractJSONObject(output)), &response); err != nil {
		return nil, fmt.Errorf("decode grafana log query plan: %w", err)
	}

	var queries []GrafanaLogPlannedQuery
	for _, query := range response.Queries {
		query.FailureRef = strings.TrimSpace(query.FailureRef)
		query.TestName = strings.TrimSpace(query.TestName)
		query.BackendArea = strings.TrimSpace(query.BackendArea)
		query.ExpectedError = strings.TrimSpace(query.ExpectedError)
		query.SearchTerms = cleanStringSlice(query.SearchTerms, 8)
		query.LogQL = strings.TrimSpace(query.LogQL)
		query.Reason = strings.TrimSpace(query.Reason)
		query.Confidence = normalizeGrafanaConfidence(query.Confidence)
		if query.FailureRef == "" || query.LogQL == "" {
			continue
		}
		queries = append(queries, query)
	}
	return queries, nil
}

func cleanStringSlice(values []string, limit int) []string {
	seen := map[string]bool{}
	var cleaned []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
		if limit > 0 && len(cleaned) >= limit {
			break
		}
	}
	return cleaned
}

func normalizeGrafanaConfidence(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func extractJSONObject(output string) string {
	trimmed := strings.TrimSpace(extractJSONText(output))
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(trimmed[start : end+1])
	}
	return trimmed
}

func renderAIInputWithOptions(analysis Analysis, options AIInputOptions) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Test run: %s\n", analysis.Current.Name))
	sb.WriteString(fmt.Sprintf("Totals: %d passed, %d failed, %d skipped\n\n", analysis.Stats.Passed, analysis.Stats.Failed, analysis.Stats.Skipped))

	if analysis.Compare != nil {
		renderAIComparison(&sb, analysis.Compare, options)
	}

	renderAIGrafanaLogs(&sb, analysis.GrafanaLogs)

	if len(analysis.Failures) > 0 {
		renderAITestListHeader(&sb, "Failed tests", len(analysis.Failures), options.MaxFailures)
	}
	for _, failure := range limitAITests(analysis.Failures, options.MaxFailures) {
		sb.WriteString(fmt.Sprintf("Test: %s\n", failure.Name))
		if failure.Suite != "" {
			sb.WriteString(fmt.Sprintf("Suite: %s\n", failure.Suite))
		}
		if location := formatLocation(failure); location != "" {
			sb.WriteString(fmt.Sprintf("Location: %s\n", location))
		}
		if failure.Message != "" {
			sb.WriteString(fmt.Sprintf("Error: %s\n", truncate(failure.Message, 2000)))
		}
		if failure.Output != "" {
			sb.WriteString(fmt.Sprintf("Output: %s\n", truncate(failure.Output, 2000)))
		}
		sb.WriteString("\n")
	}
	if omitted := omittedAITestCount(len(analysis.Failures), options.MaxFailures); omitted > 0 {
		sb.WriteString(fmt.Sprintf("%d additional failed tests omitted from AI input.\n\n", omitted))
	}

	if len(analysis.Skipped) > 0 {
		renderAITestListHeader(&sb, "Skipped tests", len(analysis.Skipped), options.MaxSkips)
	}
	for _, skipped := range limitAITests(analysis.Skipped, options.MaxSkips) {
		sb.WriteString(fmt.Sprintf("Test: %s\n", skipped.Name))
		if skipped.Suite != "" {
			sb.WriteString(fmt.Sprintf("Suite: %s\n", skipped.Suite))
		}
		if location := formatLocation(skipped); location != "" {
			sb.WriteString(fmt.Sprintf("Location: %s\n", location))
		}
		if skipped.Message != "" {
			sb.WriteString(fmt.Sprintf("Reason: %s\n", truncate(skipped.Message, 1000)))
		}
		sb.WriteString("\n")
	}
	if omitted := omittedAITestCount(len(analysis.Skipped), options.MaxSkips); omitted > 0 {
		sb.WriteString(fmt.Sprintf("%d additional skipped tests omitted from AI input.\n\n", omitted))
	}

	return sb.String()
}

func renderAIComparison(sb *strings.Builder, comparison *Comparison, options AIInputOptions) {
	sb.WriteString("Previous result comparison:\n")
	sb.WriteString(fmt.Sprintf("New failures: %d\n", len(comparison.NewFailures)))
	sb.WriteString(fmt.Sprintf("Recurring failures: %d\n", len(comparison.RecurringFailures)))
	sb.WriteString(fmt.Sprintf("Resolved failures: %d\n", len(comparison.ResolvedFailures)))
	sb.WriteString(fmt.Sprintf("New skips: %d\n", len(comparison.NewSkips)))
	sb.WriteString(fmt.Sprintf("Recurring skips: %d\n", len(comparison.RecurringSkips)))
	sb.WriteString(fmt.Sprintf("Resolved skips: %d\n", len(comparison.ResolvedSkips)))
	sb.WriteString(fmt.Sprintf("Passed delta: %+d\n", comparison.PassedDelta))
	sb.WriteString(fmt.Sprintf("Failed delta: %+d\n", comparison.FailedDelta))
	sb.WriteString(fmt.Sprintf("Skipped delta: %+d\n", comparison.SkippedDelta))
	sb.WriteString(fmt.Sprintf("Duration delta: %s\n", formatSignedDuration(comparison.DurationDelta)))

	renderAIComparisonGroup(sb, "New failure tests", comparison.NewFailures, options.MaxFailures)
	renderAIComparisonGroup(sb, "Recurring failure tests", comparison.RecurringFailures, options.MaxFailures)
	renderAIComparisonGroup(sb, "Resolved failure tests", comparison.ResolvedFailures, options.MaxFailures)
	renderAIComparisonGroup(sb, "New skipped tests", comparison.NewSkips, options.MaxSkips)
	renderAIComparisonGroup(sb, "Recurring skipped tests", comparison.RecurringSkips, options.MaxSkips)
	renderAIComparisonGroup(sb, "Resolved skipped tests", comparison.ResolvedSkips, options.MaxSkips)
	sb.WriteString("\n")
}

func renderAIGrafanaLogs(sb *strings.Builder, enrichment *GrafanaLogEnrichment) {
	if enrichment == nil || len(enrichment.Contexts) == 0 {
		return
	}

	sb.WriteString("Grafana/Loki observations for final analysis:\n")
	var scope []string
	if enrichment.StartRFC3339 != "" || enrichment.EndRFC3339 != "" {
		scope = append(scope, fmt.Sprintf("time range %s to %s", enrichment.StartRFC3339, enrichment.EndRFC3339))
	}
	if enrichment.DatasourceName != "" && enrichment.DatasourceUID != "" {
		scope = append(scope, fmt.Sprintf("datasource %s (%s)", enrichment.DatasourceName, enrichment.DatasourceUID))
	} else if enrichment.DatasourceName != "" {
		scope = append(scope, fmt.Sprintf("datasource %s", enrichment.DatasourceName))
	} else if enrichment.DatasourceUID != "" {
		scope = append(scope, fmt.Sprintf("datasource %s", enrichment.DatasourceUID))
	}
	if len(scope) > 0 {
		sb.WriteString(fmt.Sprintf("Scope: %s.\n", strings.Join(scope, "; ")))
	}
	for _, context := range enrichment.Contexts {
		testName := firstNonEmpty(context.TestName, "General lookup")
		if context.Test != nil {
			testName = firstNonEmpty(context.Test.Name, context.Test.ID, testName)
		}
		sb.WriteString(fmt.Sprintf("- Test: %s", truncate(cleanOneLine(testName), 220)))
		if context.BackendArea != "" {
			sb.WriteString(fmt.Sprintf("; backend: %s", truncate(cleanOneLine(context.BackendArea), 80)))
		}
		if context.Confidence != "" {
			sb.WriteString(fmt.Sprintf("; confidence: %s", context.Confidence))
		}
		if context.Error != "" {
			sb.WriteString(fmt.Sprintf("; Loki lookup failed: %s\n", truncate(cleanOneLine(context.Error), 220)))
			continue
		}

		lineCount := context.LineCount
		if lineCount == 0 {
			lineCount = len(context.Entries)
		}
		if lineCount == 0 {
			sb.WriteString("; Loki returned no matching log lines")
		} else if lineCount == 1 {
			sb.WriteString("; Loki returned 1 matching log line")
		} else {
			sb.WriteString(fmt.Sprintf("; Loki returned %d matching log lines", lineCount))
		}
		if components := grafanaLogComponentSummary(context.Entries); components != "" {
			sb.WriteString(fmt.Sprintf("; components: %s", components))
		}
		if hint := grafanaLogFirstMatchHint(context); hint != "" {
			sb.WriteString(fmt.Sprintf("; %s", hint))
		}
		if signal := grafanaLogSignalSummary(context); signal != "" {
			sb.WriteString(fmt.Sprintf("; Loki signal: %s", signal))
		}
		if context.FilteredLineCount > 0 {
			sb.WriteString(fmt.Sprintf("; filtered %d Grafana/MCP self-observability line(s)", context.FilteredLineCount))
		}
		if context.Truncated {
			sb.WriteString("; results were truncated by the MCP limit")
		}
		if context.GrafanaExploreURL != "" {
			sb.WriteString("; Grafana Explore query link is included in the GitHub summary")
		}
		sb.WriteString("\n")
		if context.Reason != "" {
			sb.WriteString(fmt.Sprintf("  Lookup reason: %s\n", truncate(cleanOneLine(context.Reason), 220)))
		}
	}
	sb.WriteString("\n")
}

func grafanaLogFirstMatchHint(context GrafanaLogContext) string {
	if len(context.Entries) == 0 {
		return ""
	}
	entry := context.Entries[0]
	var parts []string
	if timestamp := formatLogTimestamp(entry.Timestamp); timestamp != "-" {
		parts = append(parts, "first match at "+timestamp)
	}
	if component := firstNonEmpty(entry.Labels["app"], entry.Labels["container"], entry.Labels["namespace"], entry.Labels["pod"]); component != "" {
		parts = append(parts, "from "+truncate(cleanOneLine(component), 80))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func grafanaLogSignalSummary(context GrafanaLogContext) string {
	if len(context.Entries) == 0 {
		return ""
	}

	if signals := grafanaLogErrorSignals(context.Entries); len(signals) > 0 {
		return "error signals: " + strings.Join(signals, ", ")
	}
	return "no explicit error string in returned rows"
}

func grafanaLogErrorSignals(entries []GrafanaLogEntry) []string {
	signalRules := []struct {
		needle string
		label  string
	}{
		{"internal_error", "INTERNAL_ERROR"},
		{"connection refused", "connection refused"},
		{"connect: connection refused", "connection refused"},
		{"provisioningstatus\":\"error", "provisioningStatus=error"},
		{"\"provisioningstatus\":\"error", "provisioningStatus=error"},
		{"timeout", "timeout"},
		{"timed out", "timeout"},
		{"failed", "failed"},
		{"\"error\"", "error"},
		{" error", "error"},
	}

	seen := map[string]bool{}
	var signals []string
	for _, entry := range entries {
		line := strings.ToLower(cleanOneLine(entry.Line))
		for _, rule := range signalRules {
			if !strings.Contains(line, rule.needle) || seen[rule.label] {
				continue
			}
			seen[rule.label] = true
			signals = append(signals, rule.label)
			if len(signals) >= 4 {
				return signals
			}
		}
	}
	return signals
}

func renderAIComparisonGroup(sb *strings.Builder, title string, tests []TestCase, limit int) {
	if len(tests) == 0 {
		return
	}
	renderAITestListHeader(sb, title, len(tests), limit)
	for _, test := range limitAITests(tests, limit) {
		sb.WriteString(fmt.Sprintf("- %s", firstNonEmpty(test.Name, test.ID)))
		if test.Suite != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", test.Suite))
		}
		if location := formatLocation(test); location != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", location))
		}
		sb.WriteString("\n")
	}
	if omitted := omittedAITestCount(len(tests), limit); omitted > 0 {
		sb.WriteString(fmt.Sprintf("- %d additional tests omitted from AI input.\n", omitted))
	}
}

func renderAITestListHeader(sb *strings.Builder, title string, count int, limit int) {
	if omittedAITestCount(count, limit) > 0 {
		sb.WriteString(fmt.Sprintf("%s (showing first %d of %d):\n", title, limit, count))
		return
	}
	sb.WriteString(title + ":\n")
}

func limitAITests(tests []TestCase, limit int) []TestCase {
	if limit <= 0 || len(tests) <= limit {
		return tests
	}
	return tests[:limit]
}

func omittedAITestCount(count int, limit int) int {
	if limit <= 0 || count <= limit {
		return 0
	}
	return count - limit
}

func parseAIAnalysis(output string) *AIAnalysis {
	before, after, found := cutAIAnalysisOnDelimiter(output)
	if !found {
		return &AIAnalysis{StepSummary: strings.TrimSpace(output)}
	}
	return &AIAnalysis{
		StepSummary:  strings.TrimSpace(before),
		SlackSummary: strings.TrimSpace(after),
	}
}

func cutAIAnalysisOnDelimiter(output string) (string, string, bool) {
	lines := strings.SplitAfter(output, "\n")
	offset := 0
	for _, line := range lines {
		lineWithoutNewline := strings.TrimSuffix(line, "\n")
		lineWithoutNewline = strings.TrimSuffix(lineWithoutNewline, "\r")
		if strings.TrimSpace(lineWithoutNewline) == aiSlackDelimiter {
			before := output[:offset]
			after := output[offset+len(line):]
			return before, after, true
		}
		offset += len(line)
	}
	return "", "", false
}
