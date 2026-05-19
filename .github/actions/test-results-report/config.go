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
	SlackBotToken         string
	SlackChannel          string
	SlackAPIURL           string
	FailOnSlackError      bool
	Title                 string
	Environment           string
	WorkflowURL           string
	ReportURL             string
	MaxFailures           int
	MaxSkips              int
	IncludeSkips          bool
	EnableAIAnalysis      bool
	ClaudeToken           string
}

func loadConfig() Config {
	return configFromEnv(os.Environ)
}

func configFromEnv(envSource interface{}) Config {
	env := normalizeEnv(envSource)

	slackWebhookURL := env["INPUT_SLACK_WEBHOOK_URL"]
	slackBotToken := env["INPUT_SLACK_BOT_TOKEN"]
	slackChannel := env["INPUT_SLACK_CHANNEL"]

	sendSlack := false
	switch strings.ToLower(firstNonEmpty(env["INPUT_SEND_SLACK"], "auto")) {
	case "true", "1", "yes":
		sendSlack = true
	case "false", "0", "no":
		sendSlack = false
	default:
		sendSlack = slackWebhookURL != "" || (slackBotToken != "" && slackChannel != "")
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
		SlackBotToken:         slackBotToken,
		SlackChannel:          slackChannel,
		SlackAPIURL:           firstNonEmpty(env["SLACK_API_URL"], "https://slack.com/api/chat.postMessage"),
		FailOnSlackError:      parseBoolDefault(env["INPUT_FAIL_ON_SLACK_ERROR"], false),
		Title:                 firstNonEmpty(env["INPUT_TITLE"], "Test Results"),
		Environment:           env["INPUT_ENVIRONMENT"],
		WorkflowURL:           firstNonEmpty(env["INPUT_WORKFLOW_URL"], defaultWorkflowURL(env)),
		ReportURL:             env["INPUT_REPORT_URL"],
		MaxFailures:           parseIntDefault(env["INPUT_MAX_FAILURES"], 5),
		MaxSkips:              parseIntDefault(env["INPUT_MAX_SKIPS"], 10),
		IncludeSkips:          parseBoolDefault(env["INPUT_INCLUDE_SKIPS"], true),
		EnableAIAnalysis:      parseBoolDefault(env["INPUT_ENABLE_AI_ANALYSIS"], false),
		ClaudeToken:           firstNonEmpty(env["INPUT_CLAUDE_TOKEN"], env["CLAUDE_CODE_OAUTH_TOKEN"]),
	}
}

func (config Config) validate() error {
	if config.TestResultsPath == "" {
		return fmt.Errorf("test-results-path is required")
	}
	if config.SendSlack && config.SlackWebhookURL == "" && (config.SlackBotToken == "" || config.SlackChannel == "") {
		return fmt.Errorf("send-slack is enabled but neither slack-webhook-url nor slack-bot-token + slack-channel was provided")
	}
	if config.CompareWithPrevious && config.PreviousResultsSource != "path" {
		return fmt.Errorf("previous-results-source %q is not supported yet; use path", config.PreviousResultsSource)
	}
	return nil
}

func normalizeEnv(envSource interface{}) map[string]string {
	result := map[string]string{}
	switch env := envSource.(type) {
	case []string:
		for _, entry := range env {
			key, value, found := strings.Cut(entry, "=")
			if found {
				result[key] = value
			}
		}
	case func() []string:
		return normalizeEnv(env())
	case map[string]string:
		for key, value := range env {
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
