package main

import (
	"fmt"
	"sort"
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

	renderGrafanaLogSummary(&sb, analysis.GrafanaLogs)

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

func renderGrafanaLogSummary(sb *strings.Builder, enrichment *GrafanaLogEnrichment) {
	if enrichment == nil || len(enrichment.Contexts) == 0 {
		return
	}

	sb.WriteString("### Grafana Log Context\n\n")
	if enrichment.DatasourceUID != "" {
		if enrichment.DatasourceName != "" {
			sb.WriteString(fmt.Sprintf("Datasource: `%s` (`%s`)\n\n", escapeMarkdown(enrichment.DatasourceName), escapeMarkdown(enrichment.DatasourceUID)))
		} else {
			sb.WriteString(fmt.Sprintf("Datasource: `%s`\n\n", escapeMarkdown(enrichment.DatasourceUID)))
		}
	}
	if enrichment.StartRFC3339 != "" || enrichment.EndRFC3339 != "" {
		sb.WriteString(fmt.Sprintf("Time range: `%s` to `%s`\n\n", escapeMarkdown(enrichment.StartRFC3339), escapeMarkdown(enrichment.EndRFC3339)))
	}

	for _, context := range enrichment.Contexts {
		title := context.QueryLabel
		if title == "" {
			title = "Query"
		}
		if context.Test != nil {
			title = fmt.Sprintf("%s: %s", title, firstNonEmpty(context.Test.Name, context.Test.ID))
		}
		sb.WriteString(fmt.Sprintf("#### %s\n\n", escapeMarkdown(title)))
		if context.Reason != "" {
			sb.WriteString(fmt.Sprintf("_Reason: %s_\n\n", escapeMarkdown(truncate(cleanOneLine(context.Reason), 300))))
		}
		renderGrafanaLogMetadata(sb, context)
		sb.WriteString(fmt.Sprintf("```logql\n%s\n```\n\n", context.Query))
		if context.GrafanaExploreURL != "" {
			sb.WriteString(fmt.Sprintf("[Open query in Grafana](%s)\n\n", context.GrafanaExploreURL))
		}
		if context.Error != "" {
			sb.WriteString(fmt.Sprintf("> Grafana MCP query failed: `%s`\n\n", escapeMarkdown(truncate(cleanOneLine(context.Error), 300))))
			continue
		}
		if len(context.Entries) == 0 {
			sb.WriteString("_No matching log lines returned._\n\n")
			continue
		}

		sb.WriteString("| Time | Labels | Message |\n")
		sb.WriteString("| --- | --- | --- |\n")
		for i, entry := range context.Entries {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("| _...and %d more_ | | |\n", len(context.Entries)-i))
				break
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
				tableCell(formatLogTimestamp(entry.Timestamp)),
				tableCell(formatLogLabels(entry.Labels)),
				tableCell(truncate(cleanOneLine(entry.Line), 300)),
			))
		}
		if context.Truncated {
			sb.WriteString("| _Results truncated by MCP limit_ | | |\n")
		}
		sb.WriteString("\n")
	}
}

func renderGrafanaLogMetadata(sb *strings.Builder, context GrafanaLogContext) {
	var metadata []string
	if context.FailureRef != "" {
		metadata = append(metadata, fmt.Sprintf("failure ref `%s`", escapeMarkdown(context.FailureRef)))
	}
	if context.TestName != "" {
		metadata = append(metadata, fmt.Sprintf("test `%s`", escapeMarkdown(truncate(cleanOneLine(context.TestName), 120))))
	}
	if context.BackendArea != "" {
		metadata = append(metadata, fmt.Sprintf("backend `%s`", escapeMarkdown(truncate(cleanOneLine(context.BackendArea), 80))))
	}
	if context.Confidence != "" {
		metadata = append(metadata, fmt.Sprintf("confidence `%s`", escapeMarkdown(context.Confidence)))
	}
	if len(metadata) > 0 {
		sb.WriteString(fmt.Sprintf("_Lookup: %s._\n\n", strings.Join(metadata, ", ")))
	}
	if context.ExpectedError != "" {
		sb.WriteString(fmt.Sprintf("_Exact failure error: `%s`_\n\n", escapeMarkdown(truncate(cleanOneLine(context.ExpectedError), 240))))
	}
	if len(context.SearchTerms) > 0 {
		terms := make([]string, 0, len(context.SearchTerms))
		for _, term := range context.SearchTerms {
			terms = append(terms, escapeMarkdown(term))
		}
		sb.WriteString(fmt.Sprintf("_Search terms: `%s`_\n\n", strings.Join(terms, "`, `")))
	}
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

func formatLogTimestamp(value string) string {
	if value == "" {
		return "-"
	}
	if nanos, err := time.ParseDuration(value + "ns"); err == nil {
		return time.Unix(0, nanos.Nanoseconds()).UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return value
}

func formatLogLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, labels[key]))
		if len(parts) >= 4 {
			break
		}
	}
	if len(keys) > len(parts) {
		parts = append(parts, fmt.Sprintf("+%d", len(keys)-len(parts)))
	}
	return strings.Join(parts, " ")
}
