package main

import (
	"fmt"
	"net/url"
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

	sb.WriteString("### Grafana Observations\n\n")
	sb.WriteString(grafanaObservationIntro(enrichment))
	sb.WriteString("| Test | Backend | Observation | Link |\n")
	sb.WriteString("| --- | --- | --- | --- |\n")

	for _, context := range enrichment.Contexts {
		testName := firstNonEmpty(context.TestName, "General lookup")
		if context.Test != nil {
			testName = firstNonEmpty(context.Test.Name, context.Test.ID, testName)
		}
		link := "-"
		if grafanaURL := grafanaSummaryURL(context.GrafanaExploreURL); grafanaURL != "" {
			link = fmt.Sprintf("[Open Grafana](%s)", grafanaURL)
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			tableCell(truncate(cleanOneLine(testName), 120)),
			tableCell(truncate(cleanOneLine(context.BackendArea), 80)),
			tableCell(grafanaObservationText(context)),
			link,
		))
	}
	sb.WriteString("\n")
}

func grafanaObservationIntro(enrichment *GrafanaLogEnrichment) string {
	var parts []string
	if enrichment.DatasourceName != "" && enrichment.DatasourceUID != "" {
		parts = append(parts, fmt.Sprintf("datasource `%s` (`%s`)", escapeMarkdown(enrichment.DatasourceName), escapeMarkdown(enrichment.DatasourceUID)))
	} else if enrichment.DatasourceName != "" {
		parts = append(parts, fmt.Sprintf("datasource `%s`", escapeMarkdown(enrichment.DatasourceName)))
	} else if enrichment.DatasourceUID != "" {
		parts = append(parts, fmt.Sprintf("datasource `%s`", escapeMarkdown(enrichment.DatasourceUID)))
	}
	if enrichment.StartRFC3339 != "" || enrichment.EndRFC3339 != "" {
		parts = append(parts, fmt.Sprintf("time range `%s` to `%s`", escapeMarkdown(enrichment.StartRFC3339), escapeMarkdown(enrichment.EndRFC3339)))
	}
	if len(parts) == 0 {
		return "_Grafana lookups ran; raw log rows and query details are omitted here._\n\n"
	}
	return fmt.Sprintf("_Grafana lookups ran against %s; raw log rows and query details are omitted here._\n\n", strings.Join(parts, ", "))
}

func grafanaObservationText(context GrafanaLogContext) string {
	if context.Error != "" {
		return "Lookup failed; details are available in the job logs"
	}

	var parts []string
	lineCount := context.LineCount
	if lineCount == 0 {
		lineCount = len(context.Entries)
	}
	if lineCount == 0 {
		parts = append(parts, "No matching log lines returned")
	} else if lineCount == 1 {
		parts = append(parts, "1 matching log line returned")
	} else {
		parts = append(parts, fmt.Sprintf("%d matching log lines returned", lineCount))
	}
	if components := grafanaLogComponentSummary(context.Entries); components != "" {
		parts = append(parts, "components: "+components)
	}
	if context.FilteredLineCount > 0 {
		parts = append(parts, fmt.Sprintf("filtered %d Grafana/MCP self-observability line(s)", context.FilteredLineCount))
	}
	if context.Truncated {
		parts = append(parts, "results truncated by limit")
	}
	return strings.Join(parts, "; ")
}

func grafanaSummaryURL(exploreURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(exploreURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/explore") {
		path = strings.TrimSuffix(path, "/explore")
	}
	if path == "" {
		path = "/"
	}
	parsed.Path = path
	return parsed.String()
}

func grafanaLogComponentSummary(entries []GrafanaLogEntry) string {
	seen := map[string]bool{}
	var components []string
	for _, entry := range entries {
		component := firstNonEmpty(entry.Labels["app"], entry.Labels["container"], entry.Labels["namespace"], entry.Labels["pod"])
		component = truncate(cleanOneLine(component), 80)
		if component == "" || seen[component] {
			continue
		}
		seen[component] = true
		components = append(components, component)
		if len(components) >= 3 {
			break
		}
	}
	return strings.Join(components, ", ")
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
	value = strings.Trim(value, `"`)
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
