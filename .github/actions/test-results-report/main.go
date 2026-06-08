package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var runAIAnalysis = runClaudeAnalysis

const grafanaPlanOnlyArg = "--grafana-plan-only"
const unikornCRPlanOnlyArg = "--unikorn-cr-plan-only"
const unikornCRCollectOnlyArg = "--unikorn-cr-collect-only"

func main() {
	config := loadConfig()
	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == grafanaPlanOnlyArg:
		err = runGrafanaQueryPlanningMode(context.Background(), config)
	case len(os.Args) > 1 && os.Args[1] == unikornCRPlanOnlyArg:
		err = runUnikornCRPlanningMode(context.Background(), config)
	case len(os.Args) > 1 && os.Args[1] == unikornCRCollectOnlyArg:
		err = runUnikornCRCollectionMode(context.Background(), config)
	default:
		err = run(context.Background(), config)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "test-results-report: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config Config) error {
	totalStarted := time.Now()
	if err := config.validate(); err != nil {
		return err
	}

	stageStarted := time.Now()
	current, err := readAndParse(config.TestResultsPath, config.Format)
	if err != nil {
		return err
	}
	logReportTiming("parse-current-results", stageStarted)

	var previous *TestRun
	if config.CompareWithPrevious && config.PreviousResultsPath != "" {
		stageStarted = time.Now()
		previousRun, err := readAndParse(config.PreviousResultsPath, config.PreviousResultsFormat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: previous results could not be parsed: %v\n", err)
		} else {
			previous = &previousRun
		}
		logReportTiming("parse-previous-results", stageStarted)
	}

	stageStarted = time.Now()
	analysis := analyze(current, previous)
	logReportTiming("analyze-results", stageStarted)

	stageStarted = time.Now()
	grafanaLogs, err := runGrafanaLogEnrichment(ctx, config, analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Grafana log enrichment skipped: %v\n", err)
	} else if grafanaLogs != nil {
		analysis.GrafanaLogs = grafanaLogs
	}
	logReportTiming("grafana-log-enrichment", stageStarted)

	stageStarted = time.Now()
	unikornCRs, err := runUnikornCREnrichment(config, analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Unikorn CR enrichment skipped: %v\n", err)
	} else if unikornCRs != nil {
		analysis.UnikornCRs = unikornCRs
	}
	logReportTiming("unikorn-cr-enrichment", stageStarted)

	stageStarted = time.Now()
	aiAnalysis, err := runAIAnalysis(ctx, config, analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: AI failure analysis skipped: %v\n", err)
	}
	aiAnalysis = ensureAIAnalysisEvidenceSignals(aiAnalysis, analysis)
	logReportTiming("ai-failure-analysis", stageStarted)

	if config.WriteStepSummary {
		stageStarted = time.Now()
		summary := renderStepSummary(analysis, RenderOptions{
			Title:           config.Title,
			Environment:     config.Environment,
			WorkflowURL:     config.WorkflowURL,
			ReportURL:       config.ReportURL,
			MaxFailures:     config.MaxFailures,
			MaxSkips:        config.MaxSkips,
			IncludeSkips:    config.IncludeSkips,
			OmitTestDetails: aiAnalysis != nil && aiAnalysis.StepSummary != "",
		})
		if aiAnalysis != nil && aiAnalysis.StepSummary != "" {
			summary += "\n" + aiAnalysis.StepSummary + "\n"
		}
		if err := appendStepSummary(config.StepSummaryPath, summary); err != nil {
			return err
		}
		logReportTiming("write-step-summary", stageStarted)
	}

	slackSent := false
	if config.SendSlack {
		stageStarted = time.Now()
		slackSummary := ""
		if aiAnalysis != nil {
			slackSummary = aiAnalysis.SlackSummary
		}
		payload := buildSlackPayload(analysis, SlackOptions{
			Title:              config.Title,
			Environment:        config.Environment,
			Branch:             config.Branch,
			Actor:              config.Actor,
			WorkflowURL:        config.WorkflowURL,
			ReportURL:          config.ReportURL,
			AIAnalysis:         slackSummary,
			MaxFailures:        config.MaxFailures,
			OmitFailureDetails: strings.TrimSpace(slackSummary) != "",
		})
		if err := sendSlack(ctx, config, payload); err != nil {
			if config.FailOnSlackError {
				return err
			}
			fmt.Fprintf(os.Stderr, "Warning: Slack notification failed: %v\n", err)
		} else {
			slackSent = true
		}
		logReportTiming("send-slack", stageStarted)
	}

	stageStarted = time.Now()
	testHistoryResult := publishTestHistory(ctx, config, current)
	for _, warning := range testHistoryResult.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: test history publishing: %s\n", warning)
	}
	emitTestHistoryShippingWarning(testHistoryResult)
	logReportTiming("test-history-publish", stageStarted)

	stageStarted = time.Now()
	if err := writeOutputs(os.Getenv("GITHUB_OUTPUT"), analysis, slackSent); err != nil {
		return err
	}
	if err := writeTestHistoryOutputs(os.Getenv("GITHUB_OUTPUT"), testHistoryResult); err != nil {
		return err
	}
	logReportTiming("write-outputs", stageStarted)

	fmt.Printf("Parsed %d tests: %d passed, %d failed, %d skipped\n",
		analysis.Stats.Total,
		analysis.Stats.Passed,
		analysis.Stats.Failed,
		analysis.Stats.Skipped,
	)
	logReportTiming("total", totalStarted)

	return nil
}

func runGrafanaQueryPlanningMode(ctx context.Context, config Config) error {
	totalStarted := time.Now()
	if err := config.validate(); err != nil {
		return err
	}

	fmt.Println("::group::Grafana MCP query planning")
	defer fmt.Println("::endgroup::")

	analysis, err := buildAnalysis(config)
	if err != nil {
		return err
	}

	planPath := firstNonEmpty(config.GrafanaQueryPlanPath, filepath.Join(os.TempDir(), "test-results-report-grafana-query-plan.json"))
	plannedQueries := []GrafanaLogPlannedQuery{}
	if config.EnableGrafanaLogs && len(analysis.Failures) > 0 && config.EnableAIAnalysis && config.ClaudeToken != "" {
		plannedQueries, err = planGrafanaLogQueries(ctx, config, analysis)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Grafana query planning failed; MCP setup will be skipped: %v\n", err)
			plannedQueries = []GrafanaLogPlannedQuery{}
		}
	} else {
		logGrafanaPlanningSkip(config, analysis)
	}

	if err := writeGrafanaLogQueryPlan(planPath, plannedQueries); err != nil {
		return err
	}
	if err := writeGrafanaPlanOutputs(os.Getenv("GITHUB_OUTPUT"), planPath, len(plannedQueries)); err != nil {
		return err
	}
	logGrafana("query plan file: %s", planPath)
	logGrafana("query planning output: queries=%d needs_mcp=%t", len(plannedQueries), len(plannedQueries) > 0)
	logReportTiming("grafana-query-planning-total", totalStarted)
	return nil
}

func buildAnalysis(config Config) (Analysis, error) {
	current, err := readAndParse(config.TestResultsPath, config.Format)
	if err != nil {
		return Analysis{}, err
	}

	var previous *TestRun
	if config.CompareWithPrevious && config.PreviousResultsPath != "" {
		previousRun, err := readAndParse(config.PreviousResultsPath, config.PreviousResultsFormat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: previous results could not be parsed: %v\n", err)
		} else {
			previous = &previousRun
		}
	}

	return analyze(current, previous), nil
}

func logReportTiming(stage string, started time.Time) {
	fmt.Printf("test-results-report timing: stage=%s duration=%s\n", stage, formatTimingDuration(time.Since(started)))
}

func formatTimingDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Second {
		return duration.Truncate(time.Millisecond).String()
	}
	return duration.Truncate(100 * time.Millisecond).String()
}

func readAndParse(path, format string) (TestRun, error) {
	resolvedPath, err := resolveResultsPath(path, format)
	if err != nil {
		return TestRun{}, err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return TestRun{}, fmt.Errorf("read test results %s: %w", resolvedPath, err)
	}
	run, err := parseTestResults(data, format)
	if err != nil {
		return TestRun{}, fmt.Errorf("parse test results %s: %w", resolvedPath, err)
	}
	return run, nil
}

func resolveResultsPath(path, format string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat test results path %s: %w", path, err)
	}
	if !info.IsDir() {
		return path, nil
	}

	var newestPath string
	var newestTime int64
	var bestPreference int
	err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		preference := resultFilePreference(entry.Name(), format)
		if entry.IsDir() || preference == 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if newestPath == "" || preference > bestPreference || (preference == bestPreference && info.ModTime().UnixNano() > newestTime) {
			newestPath = candidate
			newestTime = info.ModTime().UnixNano()
			bestPreference = preference
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk test results path %s: %w", path, err)
	}
	if newestPath == "" {
		return "", fmt.Errorf("no supported test result files found under %s", path)
	}
	return newestPath, nil
}

func resultFilePreference(name, format string) int {
	lower := strings.ToLower(name)
	switch normalizeFormat(format) {
	case formatJUnit:
		if lower == "results.xml" || lower == "junit.xml" {
			return 2
		}
	case formatPlaywrightJSON:
		if lower == "results.json" {
			return 2
		}
		if lower == "test-results.json" {
			return 1
		}
	case formatGinkgoJSON:
		if lower == "test-results.json" {
			return 2
		}
		if lower == "results.json" {
			return 1
		}
	default:
		if lower == "results.xml" ||
			lower == "junit.xml" ||
			lower == "results.json" ||
			lower == "test-results.json" {
			return 1
		}
	}
	return 0
}

func appendStepSummary(path, content string) error {
	if path == "" {
		fmt.Print(content)
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_STEP_SUMMARY: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("write GITHUB_STEP_SUMMARY: %w", err)
	}
	return nil
}

func writeOutputs(path string, analysis Analysis, slackSent bool) error {
	if path == "" {
		return nil
	}

	newFailures := 0
	recurringFailures := 0
	resolvedFailures := 0
	newSkips := 0
	recurringSkips := 0
	resolvedSkips := 0
	if analysis.Compare != nil {
		newFailures = len(analysis.Compare.NewFailures)
		recurringFailures = len(analysis.Compare.RecurringFailures)
		resolvedFailures = len(analysis.Compare.ResolvedFailures)
		newSkips = len(analysis.Compare.NewSkips)
		recurringSkips = len(analysis.Compare.RecurringSkips)
		resolvedSkips = len(analysis.Compare.ResolvedSkips)
	}

	values := []struct {
		key   string
		value string
	}{
		{"total", fmt.Sprint(analysis.Stats.Total)},
		{"passed", fmt.Sprint(analysis.Stats.Passed)},
		{"failed", fmt.Sprint(analysis.Stats.Failed)},
		{"skipped", fmt.Sprint(analysis.Stats.Skipped)},
		{"duration", formatDuration(analysis.Current.Duration)},
		{"duration-ms", fmt.Sprint(analysis.Current.Duration.Milliseconds())},
		{"conclusion", conclusion(analysis)},
		{"new-failures", fmt.Sprint(newFailures)},
		{"recurring-failures", fmt.Sprint(recurringFailures)},
		{"resolved-failures", fmt.Sprint(resolvedFailures)},
		{"new-skips", fmt.Sprint(newSkips)},
		{"recurring-skips", fmt.Sprint(recurringSkips)},
		{"resolved-skips", fmt.Sprint(resolvedSkips)},
		{"slack-sent", fmt.Sprint(slackSent)},
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()

	for _, value := range values {
		if _, err := fmt.Fprintf(file, "%s=%s\n", value.key, value.value); err != nil {
			return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
		}
	}
	return nil
}

func writeGrafanaPlanOutputs(path string, planPath string, queryCount int) error {
	if path == "" {
		return nil
	}

	values := []struct {
		key   string
		value string
	}{
		{"plan-path", planPath},
		{"query-count", fmt.Sprint(queryCount)},
		{"needs-mcp", fmt.Sprint(queryCount > 0)},
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()

	for _, value := range values {
		if _, err := fmt.Fprintf(file, "%s=%s\n", value.key, value.value); err != nil {
			return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
		}
	}
	return nil
}

func writeUnikornCRPlanOutputs(path string, planPath string, queryCount int) error {
	if path == "" {
		return nil
	}

	values := []struct {
		key   string
		value string
	}{
		{"plan-path", planPath},
		{"query-count", fmt.Sprint(queryCount)},
		{"needs-kube", fmt.Sprint(queryCount > 0)},
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()

	for _, value := range values {
		if _, err := fmt.Fprintf(file, "%s=%s\n", value.key, value.value); err != nil {
			return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
		}
	}
	return nil
}

func conclusion(analysis Analysis) string {
	if analysis.Stats.Failed > 0 {
		return "failure"
	}
	return "success"
}
