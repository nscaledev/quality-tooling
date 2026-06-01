package main

import (
	"bytes"
	"context"
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

type AIInputOptions struct {
	MaxFailures int
	MaxSkips    int
}

const aiSlackDelimiter = "<<<TEST_RESULTS_REPORT_SLACK_SUMMARY_8E5B7AE7>>>"

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
- If Grafana log context is present, use it as supporting evidence for likely reasons and next checks, but do not overstate certainty when logs are empty or only loosely related.
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
- *Validation paths* (test/false failure): 3 negative-path tests are likely side effects of the same 401 auth failure.
- *File Storage input validation* (skipped): 1 test is intentionally skipped for known bug INST-457; re-enable it once the bug is fixed.
- *Action:* Use the GitHub build summary for test-level failure reasons; refresh the token or config, then rerun one focused smoke suite.`, aiSlackDelimiter, aiSlackDelimiter)
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

	sb.WriteString("Grafana log context queried via mcp-grafana:\n")
	if enrichment.DatasourceUID != "" {
		sb.WriteString(fmt.Sprintf("Datasource UID: %s\n", enrichment.DatasourceUID))
	}
	if enrichment.DatasourceName != "" {
		sb.WriteString(fmt.Sprintf("Datasource name: %s\n", enrichment.DatasourceName))
	}
	if enrichment.StartRFC3339 != "" || enrichment.EndRFC3339 != "" {
		sb.WriteString(fmt.Sprintf("Time range: %s to %s\n", enrichment.StartRFC3339, enrichment.EndRFC3339))
	}
	for _, context := range enrichment.Contexts {
		sb.WriteString(fmt.Sprintf("Query: %s\n", context.Query))
		if context.Test != nil {
			sb.WriteString(fmt.Sprintf("Related test: %s", firstNonEmpty(context.Test.Name, context.Test.ID)))
			if context.Test.Suite != "" {
				sb.WriteString(fmt.Sprintf(" [%s]", context.Test.Suite))
			}
			sb.WriteString("\n")
		}
		if context.Error != "" {
			sb.WriteString(fmt.Sprintf("Query error: %s\n\n", truncate(cleanOneLine(context.Error), 1000)))
			continue
		}
		sb.WriteString(fmt.Sprintf("Lines returned: %d\n", context.LineCount))
		for i, entry := range context.Entries {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("- %d additional log lines omitted from AI input.\n", len(context.Entries)-i))
				break
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s %s\n",
				formatLogTimestamp(entry.Timestamp),
				formatLogLabels(entry.Labels),
				truncate(cleanOneLine(entry.Line), 800),
			))
		}
		if context.Truncated {
			sb.WriteString("- Results were truncated by the MCP limit.\n")
		}
		sb.WriteString("\n")
	}
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
