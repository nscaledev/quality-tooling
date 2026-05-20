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
- Classify each pattern as one of: infra/external, code/core logic, test/false failure, unknown/mixed.
- Use unknown/mixed when there is not enough evidence to choose a category confidently.
- Mention representative tests only when they clarify a pattern; cap examples to 2 per row.
- Focus on likely cause, blast radius, confidence, and the next check.

Use this shape:
## Test Failure Analysis

### Patterns
| Category | Area / signal | Impact | Likely cause | Confidence | Next check |
| --- | --- | ---: | --- | --- | --- |
| infra/external | Auth / 401 responses | 23 failed, 37 skipped | Expired or invalid API token blocked setup-dependent specs | High | Validate the API token, then rerun one representative suite |

### Suggested Next Checks
- Confirm whether the failures share the same status/error before opening individual test issues.
- Rerun one representative failing suite after credentials or environment config are refreshed.

%s
Section 2: Plain text Slack summary.
- 4-6 high-signal Slack mrkdwn bullet lines.
- Each pattern bullet must start with '- *<suite/category>* (<category>):', where category is one of infra/external, code/core logic, test/false failure, unknown/mixed.
- Group by suite name when one suite is affected, or by a clear category name when multiple suites share the same root cause.
- Lead with the highest-attention real product, infra, or environment blocker; keep temporary sentinel/test-validation failures short unless they are the only issue.
- Include only the evidence needed to justify the category; avoid selector names, file paths, and retry details unless they materially change the next action.
- Use at most one supporting bullet such as '- *Evidence:*' or '- *Impact:*' when it makes Slack easier to act on.
- For intentional or sentinel test failures, use one short phrase that says it is temporary and should be removed or disabled before review; do not mention issue alerting unless it appears in the evidence.
- Do not list every failed or skipped test.
- Do not restate the test run title, environment, branch, actor, or full totals line; Slack already shows those fields.
- End with exactly one '- *Action:*' bullet.
- When failed tests are present, the Action bullet must mention that test-level failure reasons are available in the GitHub build summary before the next action.
- Do not mention test-level failure reasons for skip-only runs.

Use this shape:
- *Auth / all suites* (infra/external): 23 failures and 37 skips appear blocked by 401 responses from expired or invalid API credentials.
- *Impact:* Multiple setup-dependent suites are blocked before product-level assertions run.
- *Validation paths* (test/false failure): 3 negative-path tests are likely side effects of the same 401 auth failure.
- *Action:* Use the GitHub build summary for test-level failure reasons; refresh the token or config, then rerun one focused smoke suite.`, aiSlackDelimiter, aiSlackDelimiter)
}

func renderAIInputWithOptions(analysis Analysis, options AIInputOptions) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Test run: %s\n", analysis.Current.Name))
	sb.WriteString(fmt.Sprintf("Totals: %d passed, %d failed, %d skipped\n\n", analysis.Stats.Passed, analysis.Stats.Failed, analysis.Stats.Skipped))

	if analysis.Compare != nil {
		renderAIComparison(&sb, analysis.Compare, options)
	}

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
