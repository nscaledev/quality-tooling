package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TestResultsPath         string
	Format                  string
	PreviousResultsPath     string
	PreviousResultsFormat   string
	PreviousResultsSource   string
	CompareWithPrevious     bool
	WriteStepSummary        bool
	StepSummaryPath         string
	SendSlack               bool
	SlackWebhookURL         string
	FailOnSlackError        bool
	Title                   string
	Environment             string
	Branch                  string
	Actor                   string
	WorkflowURL             string
	ReportURL               string
	ComponentName           string
	ComponentVersion        string
	ComponentRef            string
	ComponentRepo           string
	ComponentVersionURL     string
	ComponentVersionToken   string
	ComponentVersionTimeout time.Duration
	MaxFailures             int
	MaxSkips                int
	IncludeSkips            bool
	EnableAIAnalysis        bool
	AIAnalysisTimeout       time.Duration
	ClaudeToken             string
	EnableGrafanaLogs       bool
	GrafanaURL              string
	GrafanaOrgID            string
	GrafanaMCPEndpoint      string
	GrafanaLokiUID          string
	GrafanaLokiName         string
	GrafanaLogStart         string
	GrafanaLogEnd           string
	GrafanaLogLookback      string
	GrafanaLogLimit         int
	GrafanaLogMaxFailures   int
	GrafanaLogConcurrency   int
	GrafanaQueryPlanPath    string
	EnableUnikornCRs        bool
	UnikornCRPlanPath       string
	UnikornCRContextPath    string
	UnikornCRMaxFailures    int
	UnikornCRTimeout        time.Duration
	PublishTestHistory      bool
	TestHistoryPublishMode  string
	TestHistoryAPIURL       string
	TestHistoryToken        string
	TestHistoryOTLPEndpoint string
	TestHistorySuite        string
	TestHistoryFramework    string
	TestHistoryEnv          string
	TestHistoryRepo         string
	TestHistoryBranch       string
	TestHistoryCommit       string
	TestHistoryRunID        string
	TestHistoryRunAttempt   int
	TestHistoryArtifactURL  string
	TestHistoryOutputPath   string
	TestHistoryTimeout      time.Duration
	TestHistoryRetries      int
	TestHistoryRetryDelay   time.Duration
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

	testHistoryAPIURL := firstNonEmpty(env["INPUT_TEST_HISTORY_API_URL"], env["TEST_HISTORY_API_URL"])
	testHistoryOTLPEndpoint := firstNonEmpty(env["INPUT_TEST_HISTORY_OTLP_ENDPOINT"], env["TEST_HISTORY_OTLP_ENDPOINT"])
	publishTestHistorySetting := firstNonEmpty(env["INPUT_PUBLISH_TEST_HISTORY"], "auto")
	publishTestHistory := parseAutoBool(publishTestHistorySetting, testHistoryAPIURL != "" || testHistoryOTLPEndpoint != "")
	testHistoryPublishMode := resolveTestHistoryPublishMode(env["INPUT_TEST_HISTORY_PUBLISH_MODE"], publishTestHistorySetting, testHistoryAPIURL, testHistoryOTLPEndpoint)

	return Config{
		TestResultsPath:         env["INPUT_TEST_RESULTS_PATH"],
		Format:                  firstNonEmpty(env["INPUT_FORMAT"], "auto"),
		PreviousResultsPath:     env["INPUT_PREVIOUS_RESULTS_PATH"],
		PreviousResultsFormat:   firstNonEmpty(env["INPUT_PREVIOUS_RESULTS_FORMAT"], env["INPUT_FORMAT"], "auto"),
		PreviousResultsSource:   firstNonEmpty(env["INPUT_PREVIOUS_RESULTS_SOURCE"], "path"),
		CompareWithPrevious:     compareWithPrevious,
		WriteStepSummary:        parseBoolDefault(env["INPUT_WRITE_STEP_SUMMARY"], true),
		StepSummaryPath:         env["GITHUB_STEP_SUMMARY"],
		SendSlack:               sendSlack,
		SlackWebhookURL:         slackWebhookURL,
		FailOnSlackError:        parseBoolDefault(env["INPUT_FAIL_ON_SLACK_ERROR"], false),
		Title:                   firstNonEmpty(env["INPUT_TITLE"], "Test Results"),
		Environment:             env["INPUT_ENVIRONMENT"],
		Branch:                  firstNonEmpty(env["INPUT_BRANCH"], env["GITHUB_REF_NAME"]),
		Actor:                   firstNonEmpty(env["INPUT_ACTOR"], env["GITHUB_ACTOR"]),
		WorkflowURL:             firstNonEmpty(env["INPUT_WORKFLOW_URL"], defaultWorkflowURL(env)),
		ReportURL:               env["INPUT_REPORT_URL"],
		ComponentName:           env["INPUT_COMPONENT_NAME"],
		ComponentVersion:        env["INPUT_COMPONENT_VERSION"],
		ComponentRef:            env["INPUT_COMPONENT_REF"],
		ComponentRepo:           firstNonEmpty(env["INPUT_COMPONENT_REPO"], env["GITHUB_REPOSITORY"]),
		ComponentVersionURL:     env["INPUT_COMPONENT_VERSION_URL"],
		ComponentVersionToken:   env["INPUT_COMPONENT_VERSION_TOKEN"],
		ComponentVersionTimeout: time.Duration(parseIntDefault(env["INPUT_COMPONENT_VERSION_TIMEOUT_SECONDS"], 10)) * time.Second,
		MaxFailures:             parseIntDefault(env["INPUT_MAX_FAILURES"], 10),
		MaxSkips:                parseIntDefault(env["INPUT_MAX_SKIPS"], 10),
		IncludeSkips:            parseBoolDefault(env["INPUT_INCLUDE_SKIPS"], true),
		EnableAIAnalysis:        parseBoolDefault(env["INPUT_ENABLE_AI_ANALYSIS"], false),
		AIAnalysisTimeout:       time.Duration(parseIntDefault(env["INPUT_AI_ANALYSIS_TIMEOUT_SECONDS"], 300)) * time.Second,
		ClaudeToken:             firstNonEmpty(env["INPUT_CLAUDE_TOKEN"], env["CLAUDE_CODE_OAUTH_TOKEN"]),
		EnableGrafanaLogs:       parseBoolDefault(env["INPUT_ENABLE_GRAFANA_LOG_ENRICHMENT"], false),
		GrafanaURL:              firstNonEmpty(env["INPUT_GRAFANA_URL"], env["GRAFANA_REPORT_URL"], env["GRAFANA_URL"]),
		GrafanaOrgID:            firstNonEmpty(env["INPUT_GRAFANA_ORG_ID"], env["GRAFANA_ORG_ID"], "1"),
		GrafanaMCPEndpoint:      firstNonEmpty(env["INPUT_GRAFANA_MCP_ENDPOINT"], env["GRAFANA_MCP_ENDPOINT"]),
		GrafanaLokiUID:          env["INPUT_GRAFANA_LOKI_DATASOURCE_UID"],
		GrafanaLokiName:         firstNonEmpty(env["INPUT_GRAFANA_LOKI_DATASOURCE_NAME"], "Loki"),
		GrafanaLogStart:         env["INPUT_GRAFANA_LOG_START"],
		GrafanaLogEnd:           env["INPUT_GRAFANA_LOG_END"],
		GrafanaLogLookback:      firstNonEmpty(env["INPUT_GRAFANA_LOG_LOOKBACK"], "2h"),
		GrafanaLogLimit:         parseIntDefault(env["INPUT_GRAFANA_LOG_LIMIT"], 20),
		GrafanaLogMaxFailures:   parseIntDefault(env["INPUT_GRAFANA_LOG_MAX_FAILURES"], 6),
		GrafanaLogConcurrency:   parseIntDefault(env["INPUT_GRAFANA_LOG_CONCURRENCY"], 4),
		GrafanaQueryPlanPath:    env["INPUT_GRAFANA_QUERY_PLAN_PATH"],
		EnableUnikornCRs:        parseBoolDefault(env["INPUT_ENABLE_UNIKORN_CR_ENRICHMENT"], false),
		UnikornCRPlanPath:       env["INPUT_UNIKORN_CR_PLAN_PATH"],
		UnikornCRContextPath:    env["INPUT_UNIKORN_CR_CONTEXT_PATH"],
		UnikornCRMaxFailures:    parseIntDefault(env["INPUT_UNIKORN_CR_MAX_FAILURES"], 4),
		UnikornCRTimeout:        time.Duration(parseIntDefault(env["INPUT_UNIKORN_CR_TIMEOUT_SECONDS"], 30)) * time.Second,
		PublishTestHistory:      publishTestHistory,
		TestHistoryPublishMode:  testHistoryPublishMode,
		TestHistoryAPIURL:       testHistoryAPIURL,
		TestHistoryToken:        firstNonEmpty(env["INPUT_TEST_HISTORY_TOKEN"], env["TEST_HISTORY_TOKEN"]),
		TestHistoryOTLPEndpoint: testHistoryOTLPEndpoint,
		TestHistorySuite:        env["INPUT_TEST_HISTORY_SUITE"],
		TestHistoryFramework:    env["INPUT_TEST_HISTORY_FRAMEWORK"],
		TestHistoryEnv:          firstNonEmpty(env["INPUT_TEST_HISTORY_ENV"], env["INPUT_ENVIRONMENT"]),
		TestHistoryRepo:         firstNonEmpty(env["INPUT_TEST_HISTORY_REPO"], env["GITHUB_REPOSITORY"], "unknown/unknown"),
		TestHistoryBranch:       firstNonEmpty(env["INPUT_TEST_HISTORY_BRANCH"], env["GITHUB_HEAD_REF"], env["INPUT_BRANCH"], env["GITHUB_REF_NAME"]),
		TestHistoryCommit:       firstNonEmpty(env["INPUT_TEST_HISTORY_COMMIT"], env["GITHUB_SHA"]),
		TestHistoryRunID:        firstNonEmpty(env["INPUT_TEST_HISTORY_RUN_ID"], env["GITHUB_RUN_ID"]),
		TestHistoryRunAttempt:   parseIntDefault(firstNonEmpty(env["INPUT_TEST_HISTORY_RUN_ATTEMPT"], env["GITHUB_RUN_ATTEMPT"]), 1),
		TestHistoryArtifactURL:  firstNonEmpty(env["INPUT_TEST_HISTORY_ARTIFACT_URL"], env["INPUT_REPORT_URL"], defaultWorkflowURL(env)),
		TestHistoryOutputPath:   firstNonEmpty(env["INPUT_TEST_HISTORY_OUTPUT_PATH"], defaultTestHistoryOutputPath(env)),
		TestHistoryTimeout:      time.Duration(parseIntDefault(env["INPUT_TEST_HISTORY_TIMEOUT_SECONDS"], 30)) * time.Second,
		TestHistoryRetries:      parseIntDefault(env["INPUT_TEST_HISTORY_RETRIES"], 1),
		TestHistoryRetryDelay:   time.Duration(parseIntDefault(env["INPUT_TEST_HISTORY_RETRY_DELAY_MS"], 5000)) * time.Millisecond,
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

func parseAutoBool(value string, auto bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	default:
		return auto
	}
}

func resolveTestHistoryPublishMode(value, publishSetting, apiURL, otlpEndpoint string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "api", "otlp":
		return mode
	}
	publish := strings.ToLower(strings.TrimSpace(publishSetting))
	if otlpEndpoint != "" || publish == "true" || publish == "1" || publish == "yes" || publish == "y" || publish == "on" {
		return "otlp"
	}
	if apiURL != "" {
		return "api"
	}
	return "otlp"
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

func defaultTestHistoryOutputPath(env map[string]string) string {
	workspace := env["GITHUB_WORKSPACE"]
	if workspace == "" {
		return filepath.Join(".test-history", "events.ndjson")
	}
	return filepath.Join(workspace, ".test-history", "events.ndjson")
}
