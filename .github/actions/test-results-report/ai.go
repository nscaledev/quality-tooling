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
	cmd.Stdin = strings.NewReader(renderAIInput(analysis))

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
	return `Analyze these test failures and skips. Output two sections separated by a line containing only '%%SLACK%%':
Section 1: A markdown step summary with '## Test Failure Analysis' heading and a concise table of failed/skipped tests, likely causes, and recommended next checks.
%%SLACK%%
Section 2: A categorised 4-5 lines plain text Slack summary. Categorise the failures and skips by likely area or pattern, call out new vs recurring signals when present, and keep each line concise.`
}

func renderAIInput(analysis Analysis) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Test run: %s\n", analysis.Current.Name))
	sb.WriteString(fmt.Sprintf("Totals: %d passed, %d failed, %d skipped\n\n", analysis.Stats.Passed, analysis.Stats.Failed, analysis.Stats.Skipped))

	if analysis.Compare != nil {
		renderAIComparison(&sb, analysis.Compare)
	}

	if len(analysis.Failures) > 0 {
		sb.WriteString("Failed tests:\n")
	}
	for _, failure := range analysis.Failures {
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

	if len(analysis.Skipped) > 0 {
		sb.WriteString("Skipped tests:\n")
	}
	for _, skipped := range analysis.Skipped {
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

	return sb.String()
}

func renderAIComparison(sb *strings.Builder, comparison *Comparison) {
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

	renderAIComparisonGroup(sb, "New failure tests", comparison.NewFailures)
	renderAIComparisonGroup(sb, "Recurring failure tests", comparison.RecurringFailures)
	renderAIComparisonGroup(sb, "Resolved failure tests", comparison.ResolvedFailures)
	renderAIComparisonGroup(sb, "New skipped tests", comparison.NewSkips)
	renderAIComparisonGroup(sb, "Recurring skipped tests", comparison.RecurringSkips)
	renderAIComparisonGroup(sb, "Resolved skipped tests", comparison.ResolvedSkips)
	sb.WriteString("\n")
}

func renderAIComparisonGroup(sb *strings.Builder, title string, tests []TestCase) {
	if len(tests) == 0 {
		return
	}
	sb.WriteString(title + ":\n")
	for _, test := range tests {
		sb.WriteString(fmt.Sprintf("- %s", firstNonEmpty(test.Name, test.ID)))
		if test.Suite != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", test.Suite))
		}
		if location := formatLocation(test); location != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", location))
		}
		sb.WriteString("\n")
	}
}

func parseAIAnalysis(output string) *AIAnalysis {
	before, after, found := strings.Cut(output, "\n%%SLACK%%\n")
	if !found {
		before, after, found = strings.Cut(output, "%%SLACK%%")
	}
	if !found {
		return &AIAnalysis{StepSummary: strings.TrimSpace(output)}
	}
	return &AIAnalysis{
		StepSummary:  strings.TrimSpace(before),
		SlackSummary: strings.TrimSpace(after),
	}
}
