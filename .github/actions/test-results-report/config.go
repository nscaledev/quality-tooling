package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	TestResultsPath       string
	Format                string
	PreviousResultsPath   string
	PreviousResultsFormat string
	PreviousResultsSource string
	CompareWithPrevious   bool
	WriteStepSummary      bool
	StepSummaryPath       string
	SendSlack             bool
	SlackWebhookURL       string
	FailOnSlackError      bool
	Title                 string
	Environment           string
	Branch                string
	Actor                 string
	WorkflowURL           string
	ReportURL             string
	MaxFailures           int
	MaxSkips              int
	IncludeSkips          bool
	EnableAIAnalysis      bool
	ClaudeToken           string
	EnableGrafanaLogs     bool
	GrafanaMCPEndpoint    string
	GrafanaLokiUID        string
	GrafanaLokiName       string
	GrafanaLogQL          string
	GrafanaLogQLTemplate  string
	GrafanaLogStart       string
	GrafanaLogEnd         string
	GrafanaLogLookback    string
	GrafanaLogLimit       int
	GrafanaLogMaxFailures int
}

func loadConfig() Config {
	return configFromEnv(envMapFromList(os.Environ()))
}

func configFromEnv(env map[string]string) Config {
	slackWebhookURL := env["INPUT_SLACK_WEBHOOK_URL"]

	sendSlack := false
	switch strings.ToLower(firstNonEmpty(env["INPUT_SEND_SLACK"], "auto")) {
	case "true", "1", "yes":
		sendSlack = true
	case "false", "0", "no":
		sendSlack = false
	default:
		sendSlack = slackWebhookURL != ""
	}

	compareWithPrevious := false
	switch strings.ToLower(firstNonEmpty(env["INPUT_COMPARE_WITH_PREVIOUS"], "auto")) {
	case "true", "1", "yes":
		compareWithPrevious = true
	case "false", "0", "no":
		compareWithPrevious = false
	default:
		compareWithPrevious = env["INPUT_PREVIOUS_RESULTS_PATH"] != ""
	}

	return Config{
		TestResultsPath:       env["INPUT_TEST_RESULTS_PATH"],
		Format:                firstNonEmpty(env["INPUT_FORMAT"], "auto"),
		PreviousResultsPath:   env["INPUT_PREVIOUS_RESULTS_PATH"],
		PreviousResultsFormat: firstNonEmpty(env["INPUT_PREVIOUS_RESULTS_FORMAT"], env["INPUT_FORMAT"], "auto"),
		PreviousResultsSource: firstNonEmpty(env["INPUT_PREVIOUS_RESULTS_SOURCE"], "path"),
		CompareWithPrevious:   compareWithPrevious,
		WriteStepSummary:      parseBoolDefault(env["INPUT_WRITE_STEP_SUMMARY"], true),
		StepSummaryPath:       env["GITHUB_STEP_SUMMARY"],
		SendSlack:             sendSlack,
		SlackWebhookURL:       slackWebhookURL,
		FailOnSlackError:      parseBoolDefault(env["INPUT_FAIL_ON_SLACK_ERROR"], false),
		Title:                 firstNonEmpty(env["INPUT_TITLE"], "Test Results"),
		Environment:           env["INPUT_ENVIRONMENT"],
		Branch:                firstNonEmpty(env["INPUT_BRANCH"], env["GITHUB_REF_NAME"]),
		Actor:                 firstNonEmpty(env["INPUT_ACTOR"], env["GITHUB_ACTOR"]),
		WorkflowURL:           firstNonEmpty(env["INPUT_WORKFLOW_URL"], defaultWorkflowURL(env)),
		ReportURL:             env["INPUT_REPORT_URL"],
		MaxFailures:           parseIntDefault(env["INPUT_MAX_FAILURES"], 10),
		MaxSkips:              parseIntDefault(env["INPUT_MAX_SKIPS"], 10),
		IncludeSkips:          parseBoolDefault(env["INPUT_INCLUDE_SKIPS"], true),
		EnableAIAnalysis:      parseBoolDefault(env["INPUT_ENABLE_AI_ANALYSIS"], false),
		ClaudeToken:           firstNonEmpty(env["INPUT_CLAUDE_TOKEN"], env["CLAUDE_CODE_OAUTH_TOKEN"]),
		EnableGrafanaLogs:     parseBoolDefault(env["INPUT_ENABLE_GRAFANA_LOG_ENRICHMENT"], false),
		GrafanaMCPEndpoint:    firstNonEmpty(env["INPUT_GRAFANA_MCP_ENDPOINT"], env["GRAFANA_MCP_ENDPOINT"]),
		GrafanaLokiUID:        env["INPUT_GRAFANA_LOKI_DATASOURCE_UID"],
		GrafanaLokiName:       env["INPUT_GRAFANA_LOKI_DATASOURCE_NAME"],
		GrafanaLogQL:          env["INPUT_GRAFANA_LOGQL"],
		GrafanaLogQLTemplate:  env["INPUT_GRAFANA_LOGQL_TEMPLATE"],
		GrafanaLogStart:       env["INPUT_GRAFANA_LOG_START"],
		GrafanaLogEnd:         env["INPUT_GRAFANA_LOG_END"],
		GrafanaLogLookback:    firstNonEmpty(env["INPUT_GRAFANA_LOG_LOOKBACK"], "1h"),
		GrafanaLogLimit:       parseIntDefault(env["INPUT_GRAFANA_LOG_LIMIT"], 20),
		GrafanaLogMaxFailures: parseIntDefault(env["INPUT_GRAFANA_LOG_MAX_FAILURES"], 3),
	}
}

func (config Config) validate() error {
	if config.TestResultsPath == "" {
		return fmt.Errorf("test-results-path is required")
	}
	if config.SendSlack && config.SlackWebhookURL == "" {
		return fmt.Errorf("send-slack is enabled but slack-webhook-url was not provided")
	}
	if config.CompareWithPrevious && config.PreviousResultsSource != "path" {
		return fmt.Errorf("previous-results-source %q is not supported yet; use path", config.PreviousResultsSource)
	}
	return nil
}

func envMapFromList(entries []string) map[string]string {
	result := map[string]string{}
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if found {
			result[key] = value
		}
	}
	return result
}

func defaultWorkflowURL(env map[string]string) string {
	server := firstNonEmpty(env["GITHUB_SERVER_URL"], "https://github.com")
	repository := env["GITHUB_REPOSITORY"]
	runID := env["GITHUB_RUN_ID"]
	if repository == "" || runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/actions/runs/%s", strings.TrimRight(server, "/"), repository, runID)
}

func parseBoolDefault(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseIntDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
