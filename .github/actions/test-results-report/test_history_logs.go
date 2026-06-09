package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type testHistoryLogQueryJob struct {
	Test               *TestCase
	TestName           string
	TestID             string
	FailureFingerprint string
	SearchTerm         string
	LogQL              string
	Reason             string
}

var (
	testHistoryRunIDLinePattern      = regexp.MustCompile(`\brun_id=([^;:\s]+)`)
	testHistoryRunAttemptLinePattern = regexp.MustCompile(`\brun_attempt=([^;:\s]+)`)
	testHistoryBodyTestNamePattern   = regexp.MustCompile(`test_history result \S+ run_id=[^:]+:\s*([^;]+)`)
	testHistoryAILikelyReasonPattern = regexp.MustCompile(`(?:^|;\s*)ai_likely_reason=([^;]+)`)
	testHistoryAINextCheckPattern    = regexp.MustCompile(`(?:^|;\s*)ai_next_check=([^;]+)`)
	testHistoryLogUnsafeWhitespace   = regexp.MustCompile(`\s+`)
)

func runTestHistoryLogEnrichment(ctx context.Context, config Config, analysis Analysis) (*TestHistoryLogEnrichment, error) {
	if !shouldRunTestHistoryLogLookup(config, analysis) {
		return nil, nil
	}

	fmt.Println("::group::Test history O11y lookup")
	defer fmt.Println("::endgroup::")

	jobs := buildTestHistoryLogQueryJobs(config, analysis)
	historyConfig := testHistoryLogGrafanaConfig(config)
	logGrafana("test history lookup enabled; failures=%d selected=%d endpoint_configured=%t datasource_uid=%s datasource_name=%s selector=%s lookback=%s limit=%d",
		len(analysis.Failures),
		len(jobs),
		historyConfig.GrafanaMCPEndpoint != "",
		firstNonEmpty(historyConfig.GrafanaLokiUID, "<empty>"),
		firstNonEmpty(historyConfig.GrafanaLokiName, "<empty>"),
		firstNonEmpty(config.TestHistoryLogSelector, "<empty>"),
		firstNonEmpty(config.TestHistoryLogLookback, "336h"),
		normalizedTestHistoryLogLimit(config.TestHistoryLogLimit),
	)
	if len(jobs) == 0 {
		logGrafana("skipping test history lookup because no failed tests were selected")
		return nil, nil
	}
	if historyConfig.GrafanaMCPEndpoint == "" {
		logGrafana("cannot run test history lookup because no grafana-mcp-endpoint/GRAFANA_MCP_ENDPOINT is available")
		return nil, fmt.Errorf("test history log lookup requires grafana-mcp-endpoint/GRAFANA_MCP_ENDPOINT")
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	logGrafana("connecting to mcp-grafana endpoint %s for test history lookup", safeURLForLog(historyConfig.GrafanaMCPEndpoint))
	client := newMCPHTTPClient(historyConfig.GrafanaMCPEndpoint)
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}

	uid, name, err := resolveLokiDatasourceStrict(ctx, client, historyConfig)
	if err != nil {
		return nil, err
	}
	start, end, err := testHistoryLogTimeRange(config, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	logGrafana("test history query time range %s to %s", start, end)

	enrichment := &TestHistoryLogEnrichment{
		DatasourceUID:  uid,
		DatasourceName: name,
		StartRFC3339:   start,
		EndRFC3339:     end,
		Contexts:       runTestHistoryLogQueryJobs(ctx, client, uid, start, end, config, jobs),
	}
	logGrafana("completed test history O11y lookup with %d context result(s)", len(enrichment.Contexts))
	return enrichment, nil
}

func shouldRunTestHistoryLogLookup(config Config, analysis Analysis) bool {
	return config.EnableTestHistoryLogs && len(analysis.Failures) > 0
}

func buildTestHistoryLogQueryJobs(config Config, analysis Analysis) []testHistoryLogQueryJob {
	failures := selectFailuresForGrafanaLogs(analysis, config.TestHistoryLogMaxFailures)
	jobs := make([]testHistoryLogQueryJob, 0, len(failures))
	selector := firstNonEmpty(config.TestHistoryLogSelector, `{service_name="test-results-report"}`)
	for _, failure := range failures {
		testID := firstNonEmpty(failure.ID, stableID(failure.Suite, failure.Name), failure.Name)
		testName := firstNonEmpty(failure.Name, testID)
		searchTerm := testHistoryLogSearchTerm(testName, testID)
		if searchTerm == "" {
			continue
		}
		fingerprint := testHistoryFailureFingerprint(failureExcerpt(failure))
		logql := buildTestHistoryLogQL(selector, searchTerm, config.TestHistoryRunID)
		jobs = append(jobs, testHistoryLogQueryJob{
			Test:               testCasePointer(failure),
			TestName:           testName,
			TestID:             testID,
			FailureFingerprint: fingerprint,
			SearchTerm:         searchTerm,
			LogQL:              logql,
			Reason:             "Look up previous failed test-history records for the same current failed test.",
		})
	}
	return jobs
}

func testHistoryLogGrafanaConfig(config Config) Config {
	historyConfig := config
	if config.TestHistoryLogMCPEndpoint != "" {
		historyConfig.GrafanaMCPEndpoint = config.TestHistoryLogMCPEndpoint
	} else if config.TestHistoryLogGrafanaApp != "" || config.TestHistoryLogGrafanaURL != "" {
		historyConfig.GrafanaMCPEndpoint = ""
	}
	historyConfig.GrafanaLokiUID = config.TestHistoryLogLokiUID
	historyConfig.GrafanaLokiName = firstNonEmpty(config.TestHistoryLogLokiName, "product-loki")
	return historyConfig
}

func testHistoryLogSearchTerm(testName, testID string) string {
	candidate := cleanOneLine(firstNonEmpty(testName, testID))
	if candidate == "" {
		return ""
	}
	candidate = testHistoryLogUnsafeWhitespace.ReplaceAllString(candidate, " ")
	if len([]rune(candidate)) > 180 {
		return string([]rune(candidate)[:180])
	}
	return candidate
}

func buildTestHistoryLogQL(selector, searchTerm, currentRunID string) string {
	selector = firstNonEmpty(strings.TrimSpace(selector), `{service_name="test-results-report"}`)
	parts := []string{
		selector,
		"|= " + logqlStringLiteral("test_history result failed"),
		"|= " + logqlStringLiteral(searchTerm),
	}
	if currentRunID != "" {
		parts = append(parts, "!= "+logqlStringLiteral("run_id="+currentRunID))
	}
	return strings.Join(parts, " ")
}

func logqlStringLiteral(value string) string {
	return strconv.Quote(value)
}

func runTestHistoryLogQueryJobs(ctx context.Context, client *mcpHTTPClient, datasourceUID, start, end string, config Config, jobs []testHistoryLogQueryJob) []TestHistoryLogContext {
	if len(jobs) == 0 {
		return nil
	}

	contexts := make([]TestHistoryLogContext, len(jobs))
	concurrency := normalizedGrafanaLogConcurrency(config.GrafanaLogConcurrency, len(jobs))
	logGrafana("executing %d test history Loki query job(s) with concurrency=%d", len(jobs), concurrency)
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for index, job := range jobs {
		index := index
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() {
				<-semaphore
			}()

			logGrafana("test history query job %d/%d started: test=%s logql=%s",
				index+1,
				len(jobs),
				truncate(cleanOneLine(firstNonEmpty(job.TestName, job.TestID)), 120),
				truncate(cleanOneLine(job.LogQL), 500),
			)
			grafanaContext := queryGrafanaLogs(ctx, client, datasourceUID, job.LogQL, start, end, normalizedTestHistoryLogLimit(config.TestHistoryLogLimit), job.Test, "test history lookup", job.Reason)
			contexts[index] = testHistoryLogContextFromGrafana(config, job, grafanaContext)
			logGrafana("test history query job %d/%d %s", index+1, len(jobs), testHistoryQueryFinishLogMessage(contexts[index]))
		}()
	}

	wg.Wait()
	return contexts
}

func testHistoryLogContextFromGrafana(config Config, job testHistoryLogQueryJob, grafanaContext GrafanaLogContext) TestHistoryLogContext {
	context := TestHistoryLogContext{
		Test:               job.Test,
		TestName:           job.TestName,
		TestID:             job.TestID,
		FailureFingerprint: job.FailureFingerprint,
		Query:              job.LogQL,
		SearchTerm:         job.SearchTerm,
		Reason:             job.Reason,
		RawLineCount:       grafanaContext.RawLineCount,
		FilteredLineCount:  grafanaContext.FilteredLineCount,
		Truncated:          grafanaContext.Truncated,
		Error:              grafanaContext.Error,
	}
	if context.Error != "" {
		return context
	}

	for _, entry := range grafanaContext.Entries {
		observation := testHistoryObservationFromGrafanaEntry(entry)
		if !testHistoryObservationMatches(config, job, observation, entry.Line) {
			context.FilteredLineCount++
			continue
		}
		context.Observations = append(context.Observations, observation)
	}
	context.LineCount = len(context.Observations)
	return context
}

func testHistoryObservationMatches(config Config, job testHistoryLogQueryJob, observation TestHistoryLogObservation, line string) bool {
	if observation.RunID != "" && config.TestHistoryRunID != "" && observation.RunID == config.TestHistoryRunID {
		return false
	}
	if observation.Repo != "" && config.TestHistoryRepo != "" && !strings.EqualFold(observation.Repo, config.TestHistoryRepo) {
		return false
	}
	if observation.Env != "" && config.TestHistoryEnv != "" && !strings.EqualFold(observation.Env, config.TestHistoryEnv) {
		return false
	}
	if observation.Suite != "" && config.TestHistorySuite != "" && !strings.EqualFold(observation.Suite, config.TestHistorySuite) {
		return false
	}
	if observation.TestID != "" && job.TestID != "" && observation.TestID != job.TestID {
		return false
	}
	if observation.TestName != "" && job.TestName != "" && !strings.EqualFold(observation.TestName, job.TestName) {
		return false
	}
	if observation.FailureFingerprint != "" && job.FailureFingerprint != "" && observation.FailureFingerprint != job.FailureFingerprint && observation.TestName == "" && observation.TestID == "" {
		return false
	}
	return strings.Contains(strings.ToLower(line), strings.ToLower(job.SearchTerm)) || observation.TestName != "" || observation.TestID != ""
}

func testHistoryObservationFromGrafanaEntry(entry GrafanaLogEntry) TestHistoryLogObservation {
	observation := TestHistoryLogObservation{
		Timestamp:          formatTestHistoryLogTimestamp(entry.Timestamp),
		Repo:               testHistoryEntryField(entry, "test.history.repo", "github.repository"),
		Suite:              testHistoryEntryField(entry, "test.history.suite"),
		Env:                testHistoryEntryField(entry, "test.history.env"),
		RunID:              testHistoryEntryField(entry, "test.history.run_id", "github.run_id"),
		RunAttempt:         testHistoryEntryField(entry, "test.history.run_attempt", "github.run_attempt"),
		TestID:             testHistoryEntryField(entry, "test.history.test_id"),
		TestName:           testHistoryEntryField(entry, "test.history.test_name"),
		FailureCategory:    testHistoryEntryField(entry, "test.history.failure_category"),
		FailureFingerprint: testHistoryEntryField(entry, "test.history.failure_fingerprint"),
		AILikelyReason:     testHistoryEntryField(entry, "test.history.ai.likely_reason"),
		AINextCheck:        testHistoryEntryField(entry, "test.history.ai.next_check"),
		AIMatchStrategy:    testHistoryEntryField(entry, "test.history.ai.match_strategy"),
		ArtifactURL:        testHistoryEntryField(entry, "test.history.artifact_url"),
	}
	if observation.RunID == "" {
		observation.RunID = firstRegexpCapture(testHistoryRunIDLinePattern, entry.Line)
	}
	if observation.RunAttempt == "" {
		observation.RunAttempt = firstRegexpCapture(testHistoryRunAttemptLinePattern, entry.Line)
	}
	if observation.TestName == "" {
		observation.TestName = firstRegexpCapture(testHistoryBodyTestNamePattern, entry.Line)
	}
	if observation.AILikelyReason == "" {
		observation.AILikelyReason = firstRegexpCapture(testHistoryAILikelyReasonPattern, entry.Line)
	}
	if observation.AINextCheck == "" {
		observation.AINextCheck = firstRegexpCapture(testHistoryAINextCheckPattern, entry.Line)
	}
	observation.TestName = cleanOneLine(observation.TestName)
	observation.AILikelyReason = cleanOneLine(observation.AILikelyReason)
	observation.AINextCheck = cleanOneLine(observation.AINextCheck)
	return observation
}

func testHistoryEntryField(entry GrafanaLogEntry, keys ...string) string {
	for _, metadata := range []map[string]string{entry.StructuredMetadata, entry.Parsed, entry.Labels} {
		if len(metadata) == 0 {
			continue
		}
		for _, key := range keys {
			for _, candidate := range testHistoryMetadataKeyVariants(key) {
				if value := strings.TrimSpace(metadata[candidate]); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func testHistoryMetadataKeyVariants(key string) []string {
	underscore := strings.ReplaceAll(key, ".", "_")
	return []string{
		key,
		underscore,
		strings.ReplaceAll(underscore, "-", "_"),
		strings.ToLower(key),
		strings.ToLower(underscore),
	}
}

func firstRegexpCapture(pattern *regexp.Regexp, value string) string {
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func testHistoryLogTimeRange(config Config, now time.Time) (string, string, error) {
	lookback, err := time.ParseDuration(firstNonEmpty(config.TestHistoryLogLookback, "336h"))
	if err != nil {
		return "", "", fmt.Errorf("parse test-history-log-lookback: %w", err)
	}
	end := now.UTC()
	start := end.Add(-lookback)
	return start.Format(time.RFC3339), end.Format(time.RFC3339), nil
}

func normalizedTestHistoryLogLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	return limit
}

func formatTestHistoryLogTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	nanos, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	return time.Unix(0, nanos).UTC().Format(time.RFC3339)
}

func testHistoryQueryFinishLogMessage(context TestHistoryLogContext) string {
	if context.Error != "" {
		return "failed: " + truncate(cleanOneLine(context.Error), 500)
	}
	return fmt.Sprintf("succeeded: raw_lines=%d matched_history_records=%d filtered=%d truncated=%t",
		context.RawLineCount,
		context.LineCount,
		context.FilteredLineCount,
		context.Truncated,
	)
}
