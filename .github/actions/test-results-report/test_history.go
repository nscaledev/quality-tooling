package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TestHistoryEvent struct {
	EventID   string `json:"event_id"`
	Repo      string `json:"repo"`
	Suite     string `json:"suite"`
	Framework string `json:"framework"`
	Env       string `json:"env"`

	Branch     string `json:"branch,omitempty"`
	CommitSHA  string `json:"commit,omitempty"`
	RunID      string `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`

	TestID   string `json:"test_id"`
	TestName string `json:"test_name,omitempty"`

	Status     string `json:"status"`
	DurationMS int    `json:"duration_ms"`

	AttemptIndex int `json:"attempt_index"`

	FailureCategory       string `json:"failure_category,omitempty"`
	FailureFingerprint    string `json:"failure_fingerprint,omitempty"`
	FailureMessageExcerpt string `json:"failure_message_excerpt,omitempty"`

	ArtifactURL string    `json:"artifact_url,omitempty"`
	StartedAt   time.Time `json:"started_at"`
}

type testHistoryContext struct {
	Repo        string
	Suite       string
	Framework   string
	Env         string
	Branch      string
	CommitSHA   string
	RunID       string
	RunAttempt  int
	ArtifactURL string
	StartedAt   time.Time
}

type TestHistoryPublishResult struct {
	Enabled        bool
	Mode           string
	EventCount     int
	SpoolPath      string
	Posted         bool
	ShippingStatus string
	FailureReason  string
	Warnings       []string
}

func publishTestHistory(ctx context.Context, config Config, current TestRun) TestHistoryPublishResult {
	result := TestHistoryPublishResult{
		Enabled:        config.PublishTestHistory,
		Mode:           firstNonEmpty(config.TestHistoryPublishMode, "otlp"),
		SpoolPath:      config.TestHistoryOutputPath,
		ShippingStatus: "disabled",
	}
	if !config.PublishTestHistory {
		return result
	}
	result.ShippingStatus = "pending"

	now := time.Now().UTC()
	events, err := buildTestHistoryEvents(config, current, now)
	if err != nil {
		markTestHistoryShippingFailure(&result, fmt.Sprintf("normalize events: %v", err))
		return result
	}
	result.EventCount = len(events)
	if len(events) == 0 {
		result.ShippingStatus = "no-events"
		result.Warnings = append(result.Warnings, "no test events found")
		return result
	}

	if config.TestHistoryOutputPath != "" {
		if err := writeTestHistorySpool(config.TestHistoryOutputPath, events); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("write spool: %v", err))
		}
	}

	switch result.Mode {
	case "api":
		if config.TestHistoryAPIURL == "" || config.TestHistoryToken == "" {
			markTestHistoryShippingFailure(&result, "TEST_HISTORY_API_URL and TEST_HISTORY_TOKEN are required for API ingest; wrote spool only")
			return result
		}
		if err := postTestHistoryEventsToAPI(ctx, config, events); err != nil {
			markTestHistoryShippingFailure(&result, fmt.Sprintf("API ingest failed: %v", err))
			return result
		}
	case "otlp":
		if config.TestHistoryOTLPEndpoint == "" {
			markTestHistoryShippingFailure(&result, "test-history OTLP endpoint is required for OTLP ingest; wrote spool only")
			return result
		}
		if err := postTestHistoryEventsToOTLP(ctx, config, events); err != nil {
			markTestHistoryShippingFailure(&result, fmt.Sprintf("OTLP ingest failed: %v", err))
			return result
		}
	default:
		markTestHistoryShippingFailure(&result, fmt.Sprintf("unsupported test-history publish mode %q; wrote spool only", result.Mode))
		return result
	}
	result.Posted = true
	result.ShippingStatus = "posted"
	return result
}

func markTestHistoryShippingFailure(result *TestHistoryPublishResult, reason string) {
	reason = cleanOneLine(reason)
	result.ShippingStatus = "failed"
	result.FailureReason = reason
	result.Warnings = append(result.Warnings, reason)
}

func emitTestHistoryShippingWarning(result TestHistoryPublishResult) {
	message := testHistoryShippingWarningMessage(result)
	if message == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "::warning title=%s::%s\n", workflowCommandEscape("Test history OTLP shipping failed"), workflowCommandEscape(message))
}

func testHistoryShippingWarningMessage(result TestHistoryPublishResult) string {
	if !result.Enabled || result.Mode != "otlp" || result.Posted || result.ShippingStatus != "failed" || result.FailureReason == "" {
		return ""
	}
	parts := []string{
		"Test history logs were not shipped to the agent collector",
		fmt.Sprintf("events=%d", result.EventCount),
	}
	if result.SpoolPath != "" {
		parts = append(parts, "spool="+result.SpoolPath)
	}
	parts = append(parts, "reason="+result.FailureReason)
	return cleanOneLine(strings.Join(parts, "; "))
}

func workflowCommandEscape(value string) string {
	value = cleanOneLine(value)
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	value = strings.ReplaceAll(value, "\n", "%0A")
	return value
}

func buildTestHistoryEvents(config Config, current TestRun, now time.Time) ([]TestHistoryEvent, error) {
	resolvedPath, err := resolveResultsPath(config.TestResultsPath, config.Format)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read test results %s: %w", resolvedPath, err)
	}
	format := normalizeFormat(config.Format)
	if format == formatAuto {
		detected, err := detectFormat(data)
		if err != nil {
			return nil, err
		}
		format = detected
	}

	context := buildTestHistoryContext(config, current, format, now)
	if format == formatPlaywrightJSON {
		events, err := testHistoryEventsFromPlaywright(data, context, now)
		if err != nil {
			return nil, err
		}
		return events, nil
	}
	return testHistoryEventsFromTestRun(current, context, now), nil
}

func buildTestHistoryContext(config Config, current TestRun, format string, now time.Time) testHistoryContext {
	runID := config.TestHistoryRunID
	if runID == "" {
		runID = fmt.Sprintf("local-%d", now.UnixMilli())
	}
	startedAt := current.StartTime
	if startedAt.IsZero() {
		startedAt = now
	}
	runAttempt := config.TestHistoryRunAttempt
	if runAttempt <= 0 {
		runAttempt = 1
	}
	return testHistoryContext{
		Repo:        firstNonEmpty(config.TestHistoryRepo, "unknown/unknown"),
		Suite:       firstNonEmpty(config.TestHistorySuite, current.Name, config.Title, "test-results"),
		Framework:   firstNonEmpty(config.TestHistoryFramework, testHistoryFrameworkForFormat(format)),
		Env:         firstNonEmpty(config.TestHistoryEnv, config.Environment, "unknown"),
		Branch:      config.TestHistoryBranch,
		CommitSHA:   config.TestHistoryCommit,
		RunID:       runID,
		RunAttempt:  runAttempt,
		ArtifactURL: firstNonEmpty(config.TestHistoryArtifactURL, config.ReportURL, config.WorkflowURL),
		StartedAt:   startedAt.UTC(),
	}
}

func testHistoryEventsFromTestRun(run TestRun, context testHistoryContext, now time.Time) []TestHistoryEvent {
	events := make([]TestHistoryEvent, 0, len(run.Tests))
	attemptIndexes := map[string]int{}
	for _, test := range run.Tests {
		testID := firstNonEmpty(test.ID, stableID(test.Suite, test.Name), test.Name)
		attemptIndex := attemptIndexes[testID]
		attemptIndexes[testID] = attemptIndex + 1
		startedAt := firstNonZeroTime(test.StartTime, run.StartTime, context.StartedAt, now).UTC()
		events = append(events, newTestHistoryEvent(context, testID, test.Name, test.Status, test.RawState, test.Duration, attemptIndex, failureExcerpt(test), startedAt))
	}
	return events
}

func testHistoryEventsFromPlaywright(data []byte, context testHistoryContext, now time.Time) ([]TestHistoryEvent, error) {
	var report playwrightReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse playwright json for test history: %w", err)
	}
	var events []TestHistoryEvent
	attemptIndexes := map[string]int{}
	for _, suite := range report.Suites {
		collectPlaywrightHistorySuite(suite, nil, context, now, &events, attemptIndexes)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("parse playwright json for test history: no test attempts found")
	}
	return events, nil
}

func collectPlaywrightHistorySuite(suite playwrightSuite, parents []string, context testHistoryContext, now time.Time, events *[]TestHistoryEvent, attemptIndexes map[string]int) {
	path := append(append([]string{}, parents...), suite.Title)
	path = nonEmpty(path)

	for _, child := range suite.Suites {
		collectPlaywrightHistorySuite(child, path, context, now, events, attemptIndexes)
	}

	for _, spec := range suite.Specs {
		file := firstNonEmpty(spec.File, suite.File, firstFile(path))
		suiteName := playwrightSuiteName(path, file)
		for _, test := range spec.Tests {
			project := firstNonEmpty(test.ProjectName, "default")
			testID := stableID(suiteName, spec.Title, project)
			if len(test.Results) == 0 {
				duration := time.Duration(0)
				startedAt := firstNonZeroTime(context.StartedAt, now).UTC()
				attemptIndex := attemptIndexes[testID]
				attemptIndexes[testID] = attemptIndex + 1
				*events = append(*events, newTestHistoryEvent(context, testID, spec.Title, playwrightStatus(test), test.Status, duration, attemptIndex, playwrightMessage(test), startedAt))
				continue
			}
			for _, result := range test.Results {
				attemptIndex := attemptIndexes[testID]
				attemptIndexes[testID] = attemptIndex + 1
				status := testHistoryPlaywrightResultStatus(result.Status)
				startedAt := firstNonZeroTime(parseRFC3339NanoTime(result.StartTime), context.StartedAt, now).UTC()
				duration := time.Duration(result.Duration) * time.Millisecond
				*events = append(*events, newTestHistoryEvent(context, testID, spec.Title, status, result.Status, duration, attemptIndex, playwrightResultMessage(result), startedAt))
			}
		}
	}
}

func newTestHistoryEvent(context testHistoryContext, testID string, testName string, status TestStatus, rawStatus string, duration time.Duration, attemptIndex int, excerpt string, startedAt time.Time) TestHistoryEvent {
	canonicalStatus := testHistoryStatus(status, rawStatus)
	event := TestHistoryEvent{
		EventID:      testHistoryEventID(context.Repo, context.RunID, context.RunAttempt, testID, attemptIndex),
		Repo:         context.Repo,
		Suite:        context.Suite,
		Framework:    context.Framework,
		Env:          context.Env,
		Branch:       context.Branch,
		CommitSHA:    context.CommitSHA,
		RunID:        context.RunID,
		RunAttempt:   context.RunAttempt,
		TestID:       testID,
		TestName:     testName,
		Status:       canonicalStatus,
		DurationMS:   durationMilliseconds(duration),
		AttemptIndex: attemptIndex,
		ArtifactURL:  context.ArtifactURL,
		StartedAt:    startedAt.UTC(),
	}
	if canonicalStatus == string(StatusFailed) {
		event.FailureMessageExcerpt = truncateFailureExcerpt(excerpt)
		event.FailureFingerprint = testHistoryFailureFingerprint(excerpt)
	}
	return event
}

func writeTestHistorySpool(path string, events []TestHistoryEvent) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func postTestHistoryEventsToAPI(ctx context.Context, config Config, events []TestHistoryEvent) error {
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	ingestURL, err := testHistoryIngestURL(config.TestHistoryAPIURL)
	if err != nil {
		return err
	}
	fmt.Printf("[test-history] POST %s mode=api events=%d\n", ingestURL, len(events))

	timeout := config.TestHistoryTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	retries := config.TestHistoryRetries
	if retries < 0 {
		retries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 && config.TestHistoryRetryDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(config.TestHistoryRetryDelay):
			}
		}
		resp, err := doPostTestHistoryEvents(ctx, client, ingestURL, map[string]string{
			"Authorization": "Bearer " + config.TestHistoryToken,
		}, body)
		if err != nil {
			lastErr = err
		} else {
			statusCode, responseBody := readTestHistoryResponse(resp)
			if statusCode < 300 {
				fmt.Printf("[test-history] response status=%d\n", statusCode)
				return nil
			}
			lastErr = fmt.Errorf("API returned %d: %s", statusCode, responseBody)
			if statusCode < 500 {
				break
			}
		}
		if attempt < retries {
			fmt.Printf("[test-history] ingest attempt %d failed (%v), retrying\n", attempt+1, lastErr)
		}
	}
	return lastErr
}

func postTestHistoryEventsToOTLP(ctx context.Context, config Config, events []TestHistoryEvent) error {
	body, err := json.Marshal(testHistoryOTLPLogsPayload(events))
	if err != nil {
		return fmt.Errorf("marshal OTLP logs: %w", err)
	}
	endpoint := strings.TrimSpace(config.TestHistoryOTLPEndpoint)
	if endpoint == "" {
		return fmt.Errorf("test-history OTLP endpoint is empty")
	}
	fmt.Printf("[test-history] POST %s mode=otlp log_records=%d\n", endpoint, len(events))

	timeout := config.TestHistoryTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	retries := config.TestHistoryRetries
	if retries < 0 {
		retries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 && config.TestHistoryRetryDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(config.TestHistoryRetryDelay):
			}
		}
		resp, err := doPostTestHistoryEvents(ctx, client, endpoint, nil, body)
		if err != nil {
			lastErr = err
		} else {
			statusCode, responseBody := readTestHistoryResponse(resp)
			if statusCode < 300 {
				fmt.Printf("[test-history] OTLP response status=%d\n", statusCode)
				return nil
			}
			lastErr = fmt.Errorf("OTLP collector returned %d: %s", statusCode, responseBody)
			if statusCode < 500 {
				break
			}
		}
		if attempt < retries {
			fmt.Printf("[test-history] OTLP ingest attempt %d failed (%v), retrying\n", attempt+1, lastErr)
		}
	}
	return lastErr
}

func doPostTestHistoryEvents(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	return client.Do(req)
}

func readTestHistoryResponse(resp *http.Response) (int, string) {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, cleanOneLine(string(body))
}

func writeTestHistoryOutputs(path string, result TestHistoryPublishResult) error {
	if path == "" {
		return nil
	}
	values := []struct {
		key   string
		value string
	}{
		{"test-history-enabled", fmt.Sprint(result.Enabled)},
		{"test-history-publish-mode", result.Mode},
		{"test-history-shipping-status", result.ShippingStatus},
		{"test-history-failure-reason", cleanOneLine(result.FailureReason)},
		{"test-history-events", fmt.Sprint(result.EventCount)},
		{"test-history-posted", fmt.Sprint(result.Posted)},
		{"test-history-spool-path", result.SpoolPath},
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()
	for _, value := range values {
		if _, err := fmt.Fprintf(file, "%s=%s\n", value.key, cleanOneLine(value.value)); err != nil {
			return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
		}
	}
	return nil
}

func testHistoryOTLPLogsPayload(events []TestHistoryEvent) map[string]any {
	resourceAttributes := []map[string]any{
		otlpStringAttribute("service.name", "test-results-report"),
		otlpStringAttribute("service.namespace", "qa-tooling"),
		otlpStringAttribute("telemetry.sdk.language", "go"),
	}
	if len(events) > 0 {
		first := events[0]
		resourceAttributes = append(resourceAttributes,
			otlpStringAttribute("test.history.repo", first.Repo),
			otlpStringAttribute("test.history.suite", first.Suite),
			otlpStringAttribute("test.history.framework", first.Framework),
			otlpStringAttribute("test.history.env", first.Env),
			otlpStringAttribute("github.repository", first.Repo),
			otlpStringAttribute("github.run_id", first.RunID),
			otlpIntAttribute("github.run_attempt", first.RunAttempt),
		)
	}

	logRecords := make([]map[string]any, 0, len(events))
	for _, event := range events {
		logRecords = append(logRecords, testHistoryOTLPLogRecord(event))
	}

	return map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{
				"attributes": compactOTLPAttributes(resourceAttributes),
			},
			"scopeLogs": []map[string]any{{
				"scope": map[string]any{
					"name":    "github.com/nscale/quality-tooling/test-results-report",
					"version": "0.1.0",
				},
				"logRecords": logRecords,
			}},
		}},
	}
}

func testHistoryOTLPLogRecord(event TestHistoryEvent) map[string]any {
	severityNumber, severityText := testHistoryOTLPSeverity(event.Status)
	attributes := []map[string]any{
		otlpStringAttribute("test.history.event_id", event.EventID),
		otlpStringAttribute("test.history.repo", event.Repo),
		otlpStringAttribute("test.history.suite", event.Suite),
		otlpStringAttribute("test.history.framework", event.Framework),
		otlpStringAttribute("test.history.env", event.Env),
		otlpStringAttribute("test.history.branch", event.Branch),
		otlpStringAttribute("test.history.commit", event.CommitSHA),
		otlpStringAttribute("test.history.run_id", event.RunID),
		otlpIntAttribute("test.history.run_attempt", event.RunAttempt),
		otlpStringAttribute("test.history.test_id", event.TestID),
		otlpStringAttribute("test.history.test_name", event.TestName),
		otlpStringAttribute("test.history.status", event.Status),
		otlpIntAttribute("test.history.duration_ms", event.DurationMS),
		otlpIntAttribute("test.history.attempt_index", event.AttemptIndex),
		otlpStringAttribute("test.history.failure_category", event.FailureCategory),
		otlpStringAttribute("test.history.failure_fingerprint", event.FailureFingerprint),
		otlpStringAttribute("test.history.failure_message_excerpt", event.FailureMessageExcerpt),
		otlpStringAttribute("test.history.artifact_url", event.ArtifactURL),
		otlpStringAttribute("github.repository", event.Repo),
		otlpStringAttribute("github.ref_name", event.Branch),
		otlpStringAttribute("github.sha", event.CommitSHA),
		otlpStringAttribute("github.run_id", event.RunID),
		otlpIntAttribute("github.run_attempt", event.RunAttempt),
	}

	return map[string]any{
		"timeUnixNano":         fmt.Sprintf("%d", event.StartedAt.UnixNano()),
		"severityNumber":       severityNumber,
		"severityText":         severityText,
		"body":                 map[string]any{"stringValue": testHistoryOTLPBody(event)},
		"attributes":           compactOTLPAttributes(attributes),
		"observedTimeUnixNano": fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
	}
}

func testHistoryOTLPBody(event TestHistoryEvent) string {
	switch event.Status {
	case string(StatusFailed):
		return fmt.Sprintf("test_history result failed: %s", firstNonEmpty(event.TestName, event.TestID))
	case string(StatusSkipped):
		return fmt.Sprintf("test_history result skipped: %s", firstNonEmpty(event.TestName, event.TestID))
	default:
		return fmt.Sprintf("test_history result passed: %s", firstNonEmpty(event.TestName, event.TestID))
	}
}

func testHistoryOTLPSeverity(status string) (int, string) {
	switch status {
	case string(StatusFailed):
		return 17, "ERROR"
	case string(StatusSkipped):
		return 13, "WARN"
	default:
		return 9, "INFO"
	}
}

func compactOTLPAttributes(attributes []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(attributes))
	for _, attribute := range attributes {
		key, _ := attribute["key"].(string)
		if key == "" {
			continue
		}
		value, _ := attribute["value"].(map[string]any)
		if len(value) == 0 {
			continue
		}
		result = append(result, attribute)
	}
	return result
}

func otlpStringAttribute(key, value string) map[string]any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return map[string]any{
		"key": key,
		"value": map[string]any{
			"stringValue": value,
		},
	}
}

func otlpIntAttribute(key string, value int) map[string]any {
	return map[string]any{
		"key": key,
		"value": map[string]any{
			"intValue": fmt.Sprintf("%d", value),
		},
	}
}

func testHistoryIngestURL(apiURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if trimmed == "" {
		return "", fmt.Errorf("test-history-api-url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid test-history-api-url %q", apiURL)
	}
	if strings.HasSuffix(parsed.Path, "/v1/runs/ingest") {
		return parsed.String(), nil
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/runs/ingest"
	return parsed.String(), nil
}

func testHistoryEventID(repo, runID string, runAttempt int, testID string, attemptIndex int) string {
	raw := fmt.Sprintf("%s:%s:%d:%s:%d", repo, runID, runAttempt, testID, attemptIndex)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)
}

func testHistoryFrameworkForFormat(format string) string {
	switch format {
	case formatPlaywrightJSON:
		return "playwright"
	case formatGinkgoJSON:
		return "ginkgo"
	case formatJUnit:
		return "junit"
	default:
		return "unknown"
	}
}

func testHistoryStatus(status TestStatus, raw string) string {
	switch status {
	case StatusPassed:
		return string(StatusPassed)
	case StatusFailed:
		return string(StatusFailed)
	case StatusSkipped:
		return string(StatusSkipped)
	default:
		return testHistoryStatusFromRaw(raw)
	}
}

func testHistoryStatusFromRaw(raw string) string {
	switch normalizeStatus(raw) {
	case StatusPassed:
		return string(StatusPassed)
	case StatusFailed:
		return string(StatusFailed)
	default:
		return string(StatusSkipped)
	}
}

func testHistoryPlaywrightResultStatus(raw string) TestStatus {
	switch raw {
	case "passed":
		return StatusPassed
	case "failed", "timedOut", "interrupted":
		return StatusFailed
	case "skipped":
		return StatusSkipped
	default:
		return StatusSkipped
	}
}

func playwrightResultMessage(result playwrightResult) string {
	if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
		return strings.TrimSpace(result.Error.Message)
	}
	for _, err := range result.Errors {
		if strings.TrimSpace(err.Message) != "" {
			return strings.TrimSpace(err.Message)
		}
	}
	return ""
}

func failureExcerpt(test TestCase) string {
	if test.Status != StatusFailed {
		return ""
	}
	return firstNonEmpty(test.Message, test.Output)
}

func truncateFailureExcerpt(value string) string {
	const maxLen = 500
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen])
}

func testHistoryFailureFingerprint(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return "sha256:" + fmt.Sprintf("%x", sum)
}

func durationMilliseconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration.Milliseconds())
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
