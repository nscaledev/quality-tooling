package main

import (
	"fmt"
	"strings"
	"time"
)

type RenderOptions struct {
	Title           string
	Environment     string
	WorkflowURL     string
	ReportURL       string
	MaxFailures     int
	MaxSkips        int
	IncludeSkips    bool
	OmitTestDetails bool
}

func renderStepSummary(analysis Analysis, options RenderOptions) string {
	options = normalizeRenderOptions(options)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s\n\n", options.Title))
	if options.Environment != "" {
		sb.WriteString(fmt.Sprintf("**Environment:** `%s`\n\n", escapeMarkdown(options.Environment)))
	}

	sb.WriteString("| Total | Passed | Failed | Skipped | Duration |\n")
	sb.WriteString("| ---: | ---: | ---: | ---: | ---: |\n")
	sb.WriteString(fmt.Sprintf("| %d | %d | %d | %d | %s |\n\n",
		analysis.Stats.Total,
		analysis.Stats.Passed,
		analysis.Stats.Failed,
		analysis.Stats.Skipped,
		formatDuration(analysis.Current.Duration),
	))

	if analysis.Compare != nil {
		renderComparison(&sb, analysis.Compare)
	}

	if options.WorkflowURL != "" || options.ReportURL != "" {
		sb.WriteString("### Links\n\n")
		if options.WorkflowURL != "" {
			sb.WriteString(fmt.Sprintf("- [GitHub workflow run](%s)\n", options.WorkflowURL))
		}
		if options.ReportURL != "" {
			sb.WriteString(fmt.Sprintf("- [Published test report](%s)\n", options.ReportURL))
		}
		sb.WriteString("\n")
	}

	if !options.OmitTestDetails {
		renderTestTable(&sb, "Failed Tests", analysis.Failures, options.MaxFailures)
		if options.IncludeSkips {
			renderTestTable(&sb, "Skipped Tests", analysis.Skipped, options.MaxSkips)
		}
	}

	return sb.String()
}

func renderComparison(sb *strings.Builder, comparison *Comparison) {
	sb.WriteString("### Previous Result Comparison\n\n")
	sb.WriteString("| New failures | Recurring failures | Resolved failures | New skips | Recurring skips | Resolved skips | Duration delta |\n")
	sb.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	sb.WriteString(fmt.Sprintf("| %d | %d | %d | %d | %d | %d | %s |\n\n",
		len(comparison.NewFailures),
		len(comparison.RecurringFailures),
		len(comparison.ResolvedFailures),
		len(comparison.NewSkips),
		len(comparison.RecurringSkips),
		len(comparison.ResolvedSkips),
		formatSignedDuration(comparison.DurationDelta),
	))
}

func renderTestTable(sb *strings.Builder, title string, tests []TestCase, limit int) {
	if len(tests) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("### %s\n\n", title))
	sb.WriteString("| Test | Suite | Location | Message |\n")
	sb.WriteString("| --- | --- | --- | --- |\n")

	for i, test := range tests {
		if i >= limit {
			sb.WriteString(fmt.Sprintf("| _...and %d more_ | | | |\n", len(tests)-limit))
			break
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			tableCell(test.Name),
			tableCell(test.Suite),
			tableCell(formatLocation(test)),
			tableCell(truncate(cleanOneLine(test.Message), 240)),
		))
	}
	sb.WriteString("\n")
}

func normalizeRenderOptions(options RenderOptions) RenderOptions {
	if options.Title == "" {
		options.Title = "Test Results"
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = 10
	}
	if options.MaxSkips <= 0 {
		options.MaxSkips = 10
	}
	return options
}

func formatLocation(test TestCase) string {
	if test.File == "" {
		return ""
	}
	if test.Line > 0 {
		return fmt.Sprintf("%s:%d", test.File, test.Line)
	}
	return test.File
}

func formatDuration(duration time.Duration) string {
	if duration == 0 {
		return "N/A"
	}
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	return fmt.Sprintf("%.1fm", duration.Minutes())
}

func formatSignedDuration(duration time.Duration) string {
	if duration == 0 {
		return "0s"
	}
	prefix := "+"
	if duration < 0 {
		prefix = "-"
		duration = -duration
	}
	return prefix + formatDuration(duration)
}

func tableCell(value string) string {
	value = cleanOneLine(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}
	return value
}

func cleanOneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func escapeMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
