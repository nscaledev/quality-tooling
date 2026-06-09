package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildTestHistoryEventsFromJUnitUsesAPIContractFields(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	if err := os.WriteFile(resultsPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="API" tests="3">
  <testcase classname="network" name="creates VPC" time="1.25">
    <failure message="POST /vpc returned 500">backend error detail</failure>
  </testcase>
  <testcase classname="network" name="creates VPC" time="0.75"/>
  <testcase classname="network" name="known skip" time="0">
    <skipped message="feature flag disabled"/>
  </testcase>
</testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	now := time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC)
	config := Config{
		TestResultsPath:        resultsPath,
		Format:                 formatJUnit,
		TestHistoryRepo:        "nscaledev/uni-region",
		TestHistorySuite:       "region-api",
		TestHistoryEnv:         "dev",
		TestHistoryBranch:      "feature/test-history",
		TestHistoryCommit:      "abc123",
		TestHistoryRunID:       "123456789",
		TestHistoryRunAttempt:  2,
		TestHistoryArtifactURL: "https://github.example/run/123456789",
	}

	events, err := buildTestHistoryEvents(config, current, now)
	if err != nil {
		t.Fatalf("buildTestHistoryEvents returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}

	failed := events[0]
	if failed.EventID != testHistoryEventID("nscaledev/uni-region", "123456789", 2, "network::creates VPC", 0) {
		t.Fatalf("event_id = %q", failed.EventID)
	}
	if failed.Repo != "nscaledev/uni-region" || failed.Suite != "region-api" || failed.Framework != "junit" || failed.Env != "dev" {
		t.Fatalf("unexpected context fields: %+v", failed)
	}
	if failed.Branch != "feature/test-history" || failed.CommitSHA != "abc123" || failed.RunID != "123456789" || failed.RunAttempt != 2 {
		t.Fatalf("unexpected run identity fields: %+v", failed)
	}
	if failed.TestID != "network::creates VPC" || failed.TestName != "creates VPC" || failed.Status != "failed" || failed.DurationMS != 1250 {
		t.Fatalf("unexpected failed event: %+v", failed)
	}
	if failed.FailureMessageExcerpt != "POST /vpc returned 500\nbackend error detail" || !strings.HasPrefix(failed.FailureFingerprint, "sha256:") {
		t.Fatalf("unexpected failure fields: %+v", failed)
	}
	if failed.ArtifactURL != "https://github.example/run/123456789" || !failed.StartedAt.Equal(now) {
		t.Fatalf("unexpected artifact/time fields: %+v", failed)
	}

	retry := events[1]
	if retry.AttemptIndex != 1 || retry.EventID != testHistoryEventID("nscaledev/uni-region", "123456789", 2, "network::creates VPC", 1) {
		t.Fatalf("duplicate JUnit test should produce attempt_index=1 and distinct id: %+v", retry)
	}
	if retry.Status != "passed" || retry.FailureMessageExcerpt != "" || retry.FailureFingerprint != "" {
		t.Fatalf("unexpected retry event fields: %+v", retry)
	}

	skipped := events[2]
	if skipped.Status != "skipped" || skipped.FailureMessageExcerpt != "" {
		t.Fatalf("skipped test should stay skipped without failure fields: %+v", skipped)
	}
}

func TestTestHistoryFailureFingerprintNormalizesVolatileTokens(t *testing.T) {
	t.Parallel()

	first := testHistoryFailureFingerprint("Timed out after 300.005s waiting for load balancer f6413693-ba3c-444e-9e80-cf2645a41f88 at /tmp/run-123/spec.go:42 on port 8443 trace 0x7ffdeadbeef at 2026-06-09T11:02:03Z")
	second := testHistoryFailureFingerprint(" timed out after 301s waiting for load balancer c9f3d569-4c74-4c90-ad6c-69033bc4702e at /private/tmp/run-999/spec.go:77 on port 9443 trace 0x123abc at 2026-06-09T11:07:20Z ")
	if first == "" || second == "" || first != second {
		t.Fatalf("expected volatile-token-normalized fingerprints to match, got %q and %q", first, second)
	}

	changed := testHistoryFailureFingerprint("connection refused while waiting for load balancer c9f3d569-4c74-4c90-ad6c-69033bc4702e")
	if changed == first {
		t.Fatalf("different logical failures should not share fingerprint %q", first)
	}
}

func TestTestHistoryStatusMappersUseSharedNormalizer(t *testing.T) {
	t.Parallel()

	rawCases := map[string]string{
		"passed":     string(StatusPassed),
		" timedOut ": string(StatusFailed),
		"timedOut":   string(StatusFailed),
		"panicked":   string(StatusFailed),
		"aborted":    string(StatusFailed),
		"pending":    string(StatusSkipped),
		"new-status": string(StatusOther),
		"":           string(StatusOther),
	}
	for raw, want := range rawCases {
		if got := testHistoryStatusFromRaw(raw); got != want {
			t.Fatalf("testHistoryStatusFromRaw(%q) = %q, want %q", raw, got, want)
		}
	}

	playwrightCases := map[string]TestStatus{
		"passed":      StatusPassed,
		"timedOut":    StatusFailed,
		"interrupted": StatusFailed,
		"skipped":     StatusSkipped,
		"new-status":  StatusOther,
	}
	for raw, want := range playwrightCases {
		if got := testHistoryPlaywrightResultStatus(raw); got != want {
			t.Fatalf("testHistoryPlaywrightResultStatus(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestBuildTestHistoryEventsFromGinkgoTreatsAbortAndPanicAsFailures(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "ginkgo.json")
	if err := os.WriteFile(resultsPath, []byte(`[
  {
    "SuiteDescription": "Ginkgo API",
    "StartTime": "2026-06-02T10:00:00Z",
    "SpecReports": [
      {
        "ContainerHierarchyTexts": ["Network Management"],
        "LeafNodeText": "panics during setup",
        "State": "panicked",
        "StartTime": "2026-06-02T10:00:01Z",
        "RunTime": 1000000000,
        "Failure": {
          "Message": "panic: nil pointer",
          "Location": {"FileName": "network_test.go", "LineNumber": 42}
        }
      },
      {
        "ContainerHierarchyTexts": ["Network Management"],
        "LeafNodeText": "aborts before cleanup",
        "State": "aborted",
        "StartTime": "2026-06-02T10:00:02Z",
        "RunTime": 2000000000,
        "Failure": {
          "Message": "suite aborted",
          "Location": {"FileName": "network_test.go", "LineNumber": 55}
        }
      }
    ]
  }
]`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatGinkgoJSON)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}
	stats := calculateStats(current.Tests)
	if stats.Failed != 2 || stats.Skipped != 0 || stats.Other != 0 {
		t.Fatalf("Ginkgo panic/abort states should be failures, got %+v", stats)
	}

	events, err := buildTestHistoryEvents(Config{
		TestResultsPath:       resultsPath,
		Format:                formatGinkgoJSON,
		TestHistoryRepo:       "nscaledev/uni-region",
		TestHistorySuite:      "region-api",
		TestHistoryEnv:        "stage",
		TestHistoryRunID:      "run-1",
		TestHistoryRunAttempt: 1,
	}, current, time.Date(2026, 6, 2, 10, 0, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildTestHistoryEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.Status != string(StatusFailed) {
			t.Fatalf("panic/abort event should publish as failed: %+v", event)
		}
		if event.FailureMessageExcerpt == "" || event.FailureFingerprint == "" {
			t.Fatalf("failed event should keep failure fields: %+v", event)
		}
	}
	if events[0].FailureMessageExcerpt != "panic: nil pointer" {
		t.Fatalf("unexpected panic failure excerpt: %+v", events[0])
	}
	if events[1].FailureMessageExcerpt != "suite aborted" {
		t.Fatalf("unexpected abort failure excerpt: %+v", events[1])
	}
}

func TestBuildTestHistoryEventsFromPlaywrightExpandsRetries(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.json")
	if err := os.WriteFile(resultsPath, []byte(`{
  "config": {"rootDir": "console"},
  "suites": [{
    "title": "spec/settings.spec.ts",
    "file": "spec/settings.spec.ts",
    "specs": [{
      "title": "saves settings",
      "line": 17,
      "tests": [{
        "projectName": "chromium",
        "status": "flaky",
	        "results": [
	          {"status": "failed", "duration": 1000, "startTime": "2026-06-02T10:00:00Z", "error": {"message": "first attempt failed"}},
	          {"status": "passed", "duration": 500, "startTime": "2026-06-02T10:00:02Z"}
	        ]
	      }, {
	        "projectName": "chromium",
	        "status": "expected",
	        "results": [
	          {"status": "passed", "duration": 300, "startTime": "2026-06-02T10:00:04Z"}
	        ]
	      }]
	    }]
	  }]
	}`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatPlaywrightJSON)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}
	stats := calculateStats(current.Tests)
	if stats.Passed != 2 || stats.Failed != 0 {
		t.Fatalf("summary parser should still treat flaky final status as passed, got %+v", stats)
	}

	events, err := buildTestHistoryEvents(Config{
		TestResultsPath:       resultsPath,
		Format:                formatPlaywrightJSON,
		TestHistoryRepo:       "nscaledev/nscale-ui",
		TestHistorySuite:      "console-e2e",
		TestHistoryEnv:        "dev",
		TestHistoryRunID:      "run-1",
		TestHistoryRunAttempt: 1,
	}, current, time.Date(2026, 6, 2, 10, 0, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildTestHistoryEvents returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	testID := "spec/settings.spec.ts::saves settings::chromium"
	if events[0].TestID != testID || events[0].Status != "failed" || events[0].AttemptIndex != 0 || events[0].DurationMS != 1000 {
		t.Fatalf("unexpected first attempt: %+v", events[0])
	}
	if events[0].StartedAt.Format(time.RFC3339) != "2026-06-02T10:00:00Z" || events[0].FailureMessageExcerpt != "first attempt failed" {
		t.Fatalf("unexpected first attempt time/failure: %+v", events[0])
	}
	if events[1].TestID != testID || events[1].Status != "passed" || events[1].AttemptIndex != 1 || events[1].DurationMS != 500 {
		t.Fatalf("unexpected retry attempt: %+v", events[1])
	}
	if events[2].TestID != testID || events[2].Status != "passed" || events[2].AttemptIndex != 2 || events[2].DurationMS != 300 {
		t.Fatalf("repeated Playwright test entry should keep incrementing attempt index: %+v", events[2])
	}
	eventIDs := map[string]bool{}
	for _, event := range events {
		if eventIDs[event.EventID] {
			t.Fatalf("Playwright attempts must have distinct deterministic ids: %+v", events)
		}
		eventIDs[event.EventID] = true
	}
}

func TestPublishTestHistoryWritesSpoolAndRetriesAPIIngest(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	spoolPath := filepath.Join(tempDir, ".test-history", "events.ndjson")
	if err := os.WriteFile(resultsPath, []byte(`<testsuite name="unit"><testcase classname="pkg" name="passes"/></testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	var attempts int32
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/runs/ingest" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected auth header: %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected content type: %q", request.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		capturedBody = body
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(writer, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":1}`))
	}))
	defer server.Close()

	result := publishTestHistory(context.Background(), Config{
		TestResultsPath:        resultsPath,
		Format:                 formatJUnit,
		PublishTestHistory:     true,
		TestHistoryPublishMode: "api",
		TestHistoryAPIURL:      server.URL,
		TestHistoryToken:       "test-token",
		TestHistoryRepo:        "nscale/repo",
		TestHistorySuite:       "unit",
		TestHistoryEnv:         "dev",
		TestHistoryRunID:       "run-1",
		TestHistoryRunAttempt:  1,
		TestHistoryOutputPath:  spoolPath,
		TestHistoryTimeout:     2 * time.Second,
		TestHistoryRetries:     1,
		TestHistoryRetryDelay:  0,
		TestHistoryArtifactURL: "https://github.example/run-1",
	}, current)

	if !result.Enabled || !result.Posted || result.EventCount != 1 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected one retry, got %d attempts", attempts)
	}
	spool := readPlainTestFile(t, spoolPath)
	if strings.Count(strings.TrimSpace(spool), "\n")+1 != 1 || !strings.Contains(spool, `"event_id"`) {
		t.Fatalf("unexpected spool content:\n%s", spool)
	}
	var requestBody struct {
		Events []TestHistoryEvent `json:"events"`
	}
	if err := json.Unmarshal(capturedBody, &requestBody); err != nil {
		t.Fatalf("decode request body: %v\n%s", err, string(capturedBody))
	}
	if len(requestBody.Events) != 1 || requestBody.Events[0].Repo != "nscale/repo" || requestBody.Events[0].ArtifactURL != "https://github.example/run-1" {
		t.Fatalf("unexpected request events: %+v", requestBody.Events)
	}
}

func TestPublishTestHistoryPostsOTLPLogs(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	spoolPath := filepath.Join(tempDir, ".test-history", "events.ndjson")
	if err := os.WriteFile(resultsPath, []byte(`<testsuite name="unit"><testcase classname="pkg" name="fails" time="0.25"><failure message="POST /instances returned 500">backend timeout</failure></testcase></testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/logs" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected content type: %q", request.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		capturedBody = body
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()

	result := publishTestHistory(context.Background(), Config{
		TestResultsPath:         resultsPath,
		Format:                  formatJUnit,
		PublishTestHistory:      true,
		TestHistoryPublishMode:  "otlp",
		TestHistoryOTLPEndpoint: server.URL + "/v1/logs",
		TestHistoryRepo:         "nscale/repo",
		TestHistorySuite:        "unit",
		TestHistoryEnv:          "dev",
		TestHistoryBranch:       "feature/test-history",
		TestHistoryCommit:       "abc123",
		TestHistoryRunID:        "run-1",
		TestHistoryRunAttempt:   2,
		TestHistoryOutputPath:   spoolPath,
		TestHistoryTimeout:      2 * time.Second,
		TestHistoryRetries:      0,
		TestHistoryArtifactURL:  "https://github.example/run-1",
	}, current)

	if !result.Enabled || result.Mode != "otlp" || !result.Posted || result.EventCount != 1 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	if spool := readPlainTestFile(t, spoolPath); !strings.Contains(spool, `"status":"failed"`) {
		t.Fatalf("spool was not written before OTLP post:\n%s", spool)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode OTLP body: %v\n%s", err, string(capturedBody))
	}
	records := otlpLogRecordsFromPayload(t, payload)
	if len(records) != 1 {
		t.Fatalf("log record count = %d, want 1", len(records))
	}
	record, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("log record = %#v", records[0])
	}
	if record["severityText"] != "ERROR" {
		t.Fatalf("severityText = %v, want ERROR", record["severityText"])
	}
	body, _ := record["body"].(map[string]any)
	if body["stringValue"] != "test_history result failed run_id=run-1: fails" {
		t.Fatalf("unexpected OTLP body: %+v", body)
	}
	attributes := otlpAttributeMap(t, record["attributes"])
	for key, want := range map[string]string{
		"test.history.repo":                "nscale/repo",
		"test.history.suite":               "unit",
		"test.history.framework":           "junit",
		"test.history.env":                 "dev",
		"test.history.branch":              "feature/test-history",
		"test.history.commit":              "abc123",
		"test.history.run_id":              "run-1",
		"test.history.run_attempt":         "2",
		"test.history.test_id":             "pkg::fails",
		"test.history.status":              "failed",
		"test.history.failure_fingerprint": testHistoryFailureFingerprint("POST /instances returned 500\nbackend timeout"),
		"github.repository":                "nscale/repo",
		"github.sha":                       "abc123",
	} {
		if got := attributes[key]; got != want {
			t.Fatalf("attribute %s = %q, want %q; all attributes: %+v", key, got, want, attributes)
		}
	}
}

func TestPublishTestHistoryEnrichesFailedOTLPLogWithAIReason(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	spoolPath := filepath.Join(tempDir, ".test-history", "events.ndjson")
	if err := os.WriteFile(resultsPath, []byte(`<testsuite name="unit"><testcase classname="pkg" name="fails" time="0.25"><failure message="POST /storage returned 404">cleanup raced after network deletion</failure></testcase></testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		capturedBody = body
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	result := publishTestHistoryWithAI(context.Background(), Config{
		TestResultsPath:         resultsPath,
		Format:                  formatJUnit,
		PublishTestHistory:      true,
		TestHistoryPublishMode:  "otlp",
		TestHistoryOTLPEndpoint: server.URL + "/v1/logs",
		TestHistoryRepo:         "nscale/repo",
		TestHistorySuite:        "unit",
		TestHistoryEnv:          "dev",
		TestHistoryRunID:        "run-1",
		TestHistoryRunAttempt:   1,
		TestHistoryOutputPath:   spoolPath,
		TestHistoryTimeout:      2 * time.Second,
		TestHistoryRetries:      0,
	}, current, &AIAnalysis{
		StepSummary: `## Test Failure Analysis

### Patterns
| Category | What failed | Why it failed | Likely reason | Impact | Next check |
| --- | --- | --- | --- | ---: | --- |
| infra/external | File storage cleanup | Delete returned 404 | File storage cleanup raced after network deletion | 1 failed | Check file-storage API/controller cleanup handling. |`,
	})

	if !result.Posted || result.EventCount != 1 {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	spool := readPlainTestFile(t, spoolPath)
	for _, expected := range []string{
		`"failure_category":"infra/external"`,
		`"ai_likely_reason":"File storage cleanup raced after network deletion"`,
		`"ai_next_check":"Check file-storage API/controller cleanup handling."`,
	} {
		if !strings.Contains(spool, expected) {
			t.Fatalf("spool missing %q:\n%s", expected, spool)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode OTLP body: %v\n%s", err, string(capturedBody))
	}
	records := otlpLogRecordsFromPayload(t, payload)
	if len(records) != 1 {
		t.Fatalf("log record count = %d, want 1", len(records))
	}
	record, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("log record = %#v", records[0])
	}
	body, _ := record["body"].(map[string]any)
	bodyText, _ := body["stringValue"].(string)
	for _, expected := range []string{
		"test_history result failed run_id=run-1: fails",
		"ai_likely_reason=File storage cleanup raced after network deletion",
		"ai_next_check=Check file-storage API/controller cleanup handling.",
	} {
		if !strings.Contains(bodyText, expected) {
			t.Fatalf("OTLP body missing %q: %s", expected, bodyText)
		}
	}
	attributes := otlpAttributeMap(t, record["attributes"])
	for key, want := range map[string]string{
		"test.history.failure_category":    "infra/external",
		"test.history.ai.category":         "infra/external",
		"test.history.ai.what_failed":      "File storage cleanup",
		"test.history.ai.why_failed":       "Delete returned 404",
		"test.history.ai.likely_reason":    "File storage cleanup raced after network deletion",
		"test.history.ai.next_check":       "Check file-storage API/controller cleanup handling.",
		"test.history.ai.match_strategy":   "single_pattern",
		"test.history.failure_fingerprint": testHistoryFailureFingerprint("POST /storage returned 404\ncleanup raced after network deletion"),
	} {
		if got := attributes[key]; got != want {
			t.Fatalf("attribute %s = %q, want %q; all attributes: %+v", key, got, want, attributes)
		}
	}
}

func TestPublishTestHistorySignalsOTLPCollectorFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	spoolPath := filepath.Join(tempDir, ".test-history", "events.ndjson")
	if err := os.WriteFile(resultsPath, []byte(`<testsuite name="unit"><testcase classname="pkg" name="passes"/></testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/logs" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		http.Error(writer, "collector unavailable\nretry later", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result := publishTestHistory(context.Background(), Config{
		TestResultsPath:         resultsPath,
		Format:                  formatJUnit,
		PublishTestHistory:      true,
		TestHistoryPublishMode:  "otlp",
		TestHistoryOTLPEndpoint: server.URL + "/v1/logs",
		TestHistoryRepo:         "nscale/repo",
		TestHistorySuite:        "unit",
		TestHistoryEnv:          "dev",
		TestHistoryRunID:        "run-1",
		TestHistoryRunAttempt:   1,
		TestHistoryOutputPath:   spoolPath,
		TestHistoryTimeout:      2 * time.Second,
		TestHistoryRetries:      0,
	}, current)

	if !result.Enabled || result.Posted || result.EventCount != 1 || result.ShippingStatus != "failed" {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	if !strings.Contains(result.FailureReason, "OTLP collector returned 503") || strings.ContainsAny(result.FailureReason, "\r\n") {
		t.Fatalf("expected single-line collector failure reason, got %q", result.FailureReason)
	}
	if warning := testHistoryShippingWarningMessage(result); !strings.Contains(warning, "not shipped to the agent collector") || !strings.Contains(warning, "events=1") || !strings.Contains(warning, "spool="+spoolPath) {
		t.Fatalf("unexpected shipping warning: %q", warning)
	}
	if spool := readPlainTestFile(t, spoolPath); !strings.Contains(spool, `"test_id":"pkg::passes"`) {
		t.Fatalf("spool was not written before OTLP failure:\n%s", spool)
	}
}

func TestPublishTestHistoryFailsOpenWhenAPICredentialsAreMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	resultsPath := filepath.Join(tempDir, "results.xml")
	spoolPath := filepath.Join(tempDir, ".test-history", "events.ndjson")
	if err := os.WriteFile(resultsPath, []byte(`<testsuite name="unit"><testcase classname="pkg" name="passes"/></testsuite>`), 0o600); err != nil {
		t.Fatalf("write results: %v", err)
	}
	current, err := readAndParse(resultsPath, formatJUnit)
	if err != nil {
		t.Fatalf("readAndParse returned error: %v", err)
	}

	result := publishTestHistory(context.Background(), Config{
		TestResultsPath:        resultsPath,
		Format:                 formatJUnit,
		PublishTestHistory:     true,
		TestHistoryPublishMode: "api",
		TestHistoryRepo:        "nscale/repo",
		TestHistorySuite:       "unit",
		TestHistoryEnv:         "dev",
		TestHistoryRunID:       "run-1",
		TestHistoryRunAttempt:  1,
		TestHistoryOutputPath:  spoolPath,
	}, current)

	if !result.Enabled || result.Posted || result.EventCount != 1 {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "wrote spool only") {
		t.Fatalf("expected missing credential warning, got %+v", result.Warnings)
	}
	if spool := readPlainTestFile(t, spoolPath); !strings.Contains(spool, `"test_id":"pkg::passes"`) {
		t.Fatalf("spool was not written before API skip:\n%s", spool)
	}
}

func TestWriteTestHistoryOutputs(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "github-output")
	err := writeTestHistoryOutputs(outputPath, TestHistoryPublishResult{
		Enabled:        true,
		Mode:           "otlp",
		ShippingStatus: "failed",
		FailureReason:  "OTLP ingest failed: collector unavailable\nretry later",
		EventCount:     3,
		SpoolPath:      "/workspace/.test-history/events.ndjson",
		Posted:         false,
	})
	if err != nil {
		t.Fatalf("writeTestHistoryOutputs returned error: %v", err)
	}
	outputs := readPlainTestFile(t, outputPath)
	for _, expected := range []string{
		"test-history-enabled=true",
		"test-history-publish-mode=otlp",
		"test-history-shipping-status=failed",
		"test-history-failure-reason=OTLP ingest failed: collector unavailable retry later",
		"test-history-events=3",
		"test-history-posted=false",
		"test-history-spool-path=/workspace/.test-history/events.ndjson",
	} {
		if !strings.Contains(outputs, expected) {
			t.Fatalf("outputs missing %q:\n%s", expected, outputs)
		}
	}
}

func otlpLogRecordsFromPayload(t *testing.T, payload map[string]any) []any {
	t.Helper()

	resourceLogs, ok := payload["resourceLogs"].([]any)
	if !ok || len(resourceLogs) != 1 {
		t.Fatalf("resourceLogs = %#v", payload["resourceLogs"])
	}
	resourceLog, ok := resourceLogs[0].(map[string]any)
	if !ok {
		t.Fatalf("resourceLog = %#v", resourceLogs[0])
	}
	scopeLogs, ok := resourceLog["scopeLogs"].([]any)
	if !ok || len(scopeLogs) != 1 {
		t.Fatalf("scopeLogs = %#v", resourceLog["scopeLogs"])
	}
	scopeLog, ok := scopeLogs[0].(map[string]any)
	if !ok {
		t.Fatalf("scopeLog = %#v", scopeLogs[0])
	}
	records, ok := scopeLog["logRecords"].([]any)
	if !ok {
		t.Fatalf("logRecords = %#v", scopeLog["logRecords"])
	}
	return records
}

func otlpAttributeMap(t *testing.T, raw any) map[string]string {
	t.Helper()

	attributes, ok := raw.([]any)
	if !ok {
		t.Fatalf("attributes = %#v", raw)
	}
	result := map[string]string{}
	for _, item := range attributes {
		attribute, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("attribute = %#v", item)
		}
		key, _ := attribute["key"].(string)
		value, _ := attribute["value"].(map[string]any)
		if stringValue, ok := value["stringValue"].(string); ok {
			result[key] = stringValue
			continue
		}
		if intValue, ok := value["intValue"].(string); ok {
			result[key] = intValue
		}
	}
	return result
}

func TestTestHistoryIngestURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "bare host appends path",
			input: "https://api.example.com",
			want:  "https://api.example.com/v1/runs/ingest",
		},
		{
			name:  "trailing slash is stripped before append",
			input: "https://api.example.com/",
			want:  "https://api.example.com/v1/runs/ingest",
		},
		{
			name:  "already complete URL is idempotent",
			input: "https://api.example.com/v1/runs/ingest",
			want:  "https://api.example.com/v1/runs/ingest",
		},
		{
			name:  "already complete URL with trailing slash is idempotent",
			input: "https://api.example.com/v1/runs/ingest/",
			want:  "https://api.example.com/v1/runs/ingest",
		},
		{
			name:    "empty string returns error",
			input:   "",
			wantErr: true,
		},
		{
			name:    "missing scheme returns error",
			input:   "api.example.com/v1/runs/ingest",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := testHistoryIngestURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("testHistoryIngestURL(%q) = %q, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("testHistoryIngestURL(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("testHistoryIngestURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
