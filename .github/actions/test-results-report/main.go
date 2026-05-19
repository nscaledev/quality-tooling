package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(context.Background(), loadConfig()); err != nil {
		fmt.Fprintf(os.Stderr, "test-results-report: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}

	current, err := readAndParse(config.TestResultsPath, config.Format)
	if err != nil {
		return err
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

	analysis := analyze(current, previous)

	aiAnalysis, err := runClaudeAnalysis(ctx, config, analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: AI failure analysis skipped: %v\n", err)
	}

	if config.WriteStepSummary {
		summary := renderStepSummary(analysis, RenderOptions{
			Title:        config.Title,
			Environment:  config.Environment,
			WorkflowURL:  config.WorkflowURL,
			ReportURL:    config.ReportURL,
			MaxFailures:  config.MaxFailures,
			MaxSkips:     config.MaxSkips,
			IncludeSkips: config.IncludeSkips,
		})
		if aiAnalysis != nil && aiAnalysis.StepSummary != "" {
			summary += "\n" + aiAnalysis.StepSummary + "\n"
		}
		if err := appendStepSummary(config.StepSummaryPath, summary); err != nil {
			return err
		}
	}

	slackSent := false
	if config.SendSlack {
		slackSummary := ""
		if aiAnalysis != nil {
			slackSummary = aiAnalysis.SlackSummary
		}
		payload := buildSlackPayload(analysis, SlackOptions{
			Title:       config.Title,
			Environment: config.Environment,
			Branch:      config.Branch,
			Actor:       config.Actor,
			WorkflowURL: config.WorkflowURL,
			ReportURL:   config.ReportURL,
			Channel:     config.SlackChannel,
			AIAnalysis:  slackSummary,
			MaxFailures: config.MaxFailures,
		})
		if err := sendSlack(ctx, config, payload); err != nil {
			if config.FailOnSlackError {
				return err
			}
			fmt.Fprintf(os.Stderr, "Warning: Slack notification failed: %v\n", err)
		} else {
			slackSent = true
		}
	}

	if err := writeOutputs(os.Getenv("GITHUB_OUTPUT"), analysis, slackSent); err != nil {
		return err
	}

	fmt.Printf("Parsed %d tests: %d passed, %d failed, %d skipped\n",
		analysis.Stats.Total,
		analysis.Stats.Passed,
		analysis.Stats.Failed,
		analysis.Stats.Skipped,
	)

	return nil
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
	err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isKnownResultsFile(entry.Name(), format) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if newestPath == "" || info.ModTime().UnixNano() > newestTime {
			newestPath = candidate
			newestTime = info.ModTime().UnixNano()
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

func isKnownResultsFile(name, format string) bool {
	lower := strings.ToLower(name)
	switch normalizeFormat(format) {
	case formatJUnit:
		return lower == "results.xml" || lower == "junit.xml"
	case formatPlaywrightJSON:
		return lower == "results.json"
	case formatGinkgoJSON:
		return lower == "test-results.json"
	default:
		return lower == "results.xml" ||
			lower == "junit.xml" ||
			lower == "results.json" ||
			lower == "test-results.json"
	}
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

	values := map[string]string{
		"total":              fmt.Sprint(analysis.Stats.Total),
		"passed":             fmt.Sprint(analysis.Stats.Passed),
		"failed":             fmt.Sprint(analysis.Stats.Failed),
		"skipped":            fmt.Sprint(analysis.Stats.Skipped),
		"duration":           formatDuration(analysis.Current.Duration),
		"duration-ms":        fmt.Sprint(analysis.Current.Duration.Milliseconds()),
		"conclusion":         conclusion(analysis),
		"slack-sent":         fmt.Sprint(slackSent),
		"new-failures":       "0",
		"recurring-failures": "0",
		"resolved-failures":  "0",
		"new-skips":          "0",
		"recurring-skips":    "0",
		"resolved-skips":     "0",
	}
	if analysis.Compare != nil {
		values["new-failures"] = fmt.Sprint(len(analysis.Compare.NewFailures))
		values["recurring-failures"] = fmt.Sprint(len(analysis.Compare.RecurringFailures))
		values["resolved-failures"] = fmt.Sprint(len(analysis.Compare.ResolvedFailures))
		values["new-skips"] = fmt.Sprint(len(analysis.Compare.NewSkips))
		values["recurring-skips"] = fmt.Sprint(len(analysis.Compare.RecurringSkips))
		values["resolved-skips"] = fmt.Sprint(len(analysis.Compare.ResolvedSkips))
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()

	for key, value := range values {
		if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
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
