package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderGrafanaLogQLTemplate(t *testing.T) {
	t.Parallel()

	query := renderGrafanaLogQLTemplate(
		`{namespace="unikorn-region"} |~ "(?i){{log_keywords_regex}}" |= "{{test_name}}"`,
		TestCase{
			Name:    `creates "quoted" instance`,
			Suite:   "Instance Management",
			File:    "test/api/suites/instance_test.go",
			Message: "Timeout waiting for instance reconcile",
		},
		Config{Environment: "dev"},
	)

	for _, expected := range []string{
		`{namespace="unikorn-region"}`,
		`creates \"quoted\" instance`,
		`instance`,
		`suites/instance_test\.go`,
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("query missing %q: %s", expected, query)
		}
	}
}

func TestLogKeywordRegexPrioritizesFailureIdentifiers(t *testing.T) {
	t.Parallel()

	keywords := logKeywordRegex(TestCase{
		Name:    "Instance Operations > When creating an instance > from a snapshot image > should launch an instance successfully",
		Suite:   "Instance Operations",
		File:    "test/api/suites/instance_operations_test.go",
		Message: "Instance dd8a7359-33e4-4613-93fd-c8816e28bdbb entered error health status with imageID=374b2103-a183-4cb4-b740-126a873ab8a5",
		Output:  "Taking snapshot of instance fbb6b2c4-6c44-4837-8b9f-3a43283e94b8",
	})

	for _, expected := range []string{
		`dd8a7359-33e4-4613-93fd-c8816e28bdbb`,
		`374b2103-a183-4cb4-b740-126a873ab8a5`,
		`fbb6b2c4-6c44-4837-8b9f-3a43283e94b8`,
	} {
		if !strings.Contains(keywords, expected) {
			t.Fatalf("keywords missing %q: %s", expected, keywords)
		}
	}
}

func TestRunGrafanaLogEnrichmentThroughMCP(t *testing.T) {
	t.Parallel()

	var sawListDatasources atomic.Bool
	var sawQueryLoki atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/mcp" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}

		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "test-session")

		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "fake-grafana-mcp"},
			})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode tool params: %v", err)
			}
			switch params.Name {
			case "list_datasources":
				sawListDatasources.Store(true)
				writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[{"uid":"loki-dev","name":"Loki","type":"loki","isDefault":true}]}`)
			case "query_loki_logs":
				sawQueryLoki.Store(true)
				var args map[string]any
				if err := json.Unmarshal(params.Arguments, &args); err != nil {
					t.Fatalf("decode query args: %v", err)
				}
				if args["datasourceUid"] != "loki-dev" {
					t.Fatalf("datasourceUid = %v", args["datasourceUid"])
				}
				if !strings.Contains(args["logql"].(string), "instance") {
					t.Fatalf("logql missing failure context: %s", args["logql"])
				}
				writeMCPToolResponse(t, writer, rpc.ID, `{"data":[{"timestamp":"1780322400000000000","line":"region controller reconcile failed","labels":{"namespace":"unikorn-region"}}],"metadata":{"linesReturned":1,"resultsTruncated":false}}`)
			default:
				t.Fatalf("unexpected tool %s", params.Name)
			}
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
		}
	}))
	defer server.Close()

	enrichment, err := runGrafanaLogEnrichment(context.Background(), Config{
		EnableGrafanaLogs:     true,
		GrafanaMCPEndpoint:    server.URL + "/mcp",
		GrafanaLogQLTemplate:  `{namespace="unikorn-region"} |~ "(?i){{log_keywords_regex}}"`,
		GrafanaLogStart:       "2026-06-01T13:00:00Z",
		GrafanaLogEnd:         "2026-06-01T14:00:00Z",
		GrafanaLogLimit:       5,
		GrafanaLogMaxFailures: 1,
	}, Analysis{
		Failures: []TestCase{{
			ID:      "instance-create",
			Name:    "creates instance",
			Suite:   "Instance Management",
			Message: "instance reconcile timed out",
		}},
	})
	if err != nil {
		t.Fatalf("runGrafanaLogEnrichment returned error: %v", err)
	}
	if !sawListDatasources.Load() || !sawQueryLoki.Load() {
		t.Fatalf("expected list and query calls, list=%v query=%v", sawListDatasources.Load(), sawQueryLoki.Load())
	}
	if enrichment.DatasourceUID != "loki-dev" || len(enrichment.Contexts) != 1 {
		t.Fatalf("unexpected enrichment: %+v", enrichment)
	}
	if len(enrichment.Contexts[0].Entries) != 1 || !strings.Contains(enrichment.Contexts[0].Entries[0].Line, "reconcile failed") {
		t.Fatalf("unexpected log entries: %+v", enrichment.Contexts[0].Entries)
	}
}

func TestDecodeListDatasourcesResultAcceptsObjectAndLiveArrayShapes(t *testing.T) {
	t.Parallel()

	objectDatasources, err := decodeListDatasourcesResult([]byte(`{"datasources":[{"uid":"loki-object","name":"Object Loki","type":"loki","isDefault":true}]}`))
	if err != nil {
		t.Fatalf("decode object datasource shape: %v", err)
	}
	if len(objectDatasources) != 1 || objectDatasources[0].UID != "loki-object" || !objectDatasources[0].IsDefault {
		t.Fatalf("unexpected object datasources: %+v", objectDatasources)
	}

	arrayDatasources, err := decodeListDatasourcesResult([]byte(`[{"id":3,"uid":"loki","name":"Loki","type":"loki","isDefault":false}]`))
	if err != nil {
		t.Fatalf("decode live array datasource shape: %v", err)
	}
	if len(arrayDatasources) != 1 || arrayDatasources[0].UID != "loki" || arrayDatasources[0].Name != "Loki" {
		t.Fatalf("unexpected array datasources: %+v", arrayDatasources)
	}

	if _, err := decodeListDatasourcesResult([]byte(`{"unexpected":true}`)); err == nil {
		t.Fatal("unexpected object shape should fail to decode")
	}
}

func TestGrafanaQueryFinishLogMessageShowsMCPFetchOutcome(t *testing.T) {
	t.Parallel()

	success := grafanaQueryFinishLogMessage(GrafanaLogContext{
		RawLineCount:      3,
		LineCount:         2,
		FilteredLineCount: 1,
		Entries: []GrafanaLogEntry{
			{Line: "network controller failed"},
			{Line: "quota exceeded"},
		},
		Truncated:         true,
		GrafanaExploreURL: "https://grafana.example.com/explore",
	})
	for _, expected := range []string{
		"succeeded; Loki returned usable log lines",
		"raw_lines=3",
		"usable_lines=2",
		"filtered_self_observability=1",
		"truncated=true",
		"grafana_url=true",
	} {
		if !strings.Contains(success, expected) {
			t.Fatalf("success log missing %q: %s", expected, success)
		}
	}

	empty := grafanaQueryFinishLogMessage(GrafanaLogContext{})
	if !strings.Contains(empty, "succeeded; Loki returned no matching log lines") || !strings.Contains(empty, "raw_lines=0") {
		t.Fatalf("empty result log was not explicit: %s", empty)
	}

	filteredOnly := grafanaQueryFinishLogMessage(GrafanaLogContext{
		RawLineCount:      2,
		LineCount:         0,
		FilteredLineCount: 2,
	})
	if !strings.Contains(filteredOnly, "succeeded; Loki returned only Grafana/MCP self-observability lines") || !strings.Contains(filteredOnly, "usable_lines=0") {
		t.Fatalf("filtered-only result log was not explicit: %s", filteredOnly)
	}
}

func TestRunGrafanaLogEnrichmentUsesAIPlannedQueries(t *testing.T) {
	previousPlanner := runGrafanaLogQueryPlanning
	runGrafanaLogQueryPlanning = func(_ context.Context, _ Config, _ Analysis) ([]GrafanaLogPlannedQuery, error) {
		return []GrafanaLogPlannedQuery{
			{
				FailureRef: "unknown-failure",
				LogQL:      `{namespace=~".+"} |~ "(?i)(unrelated|broad)"`,
				Reason:     "This hallucinated failure ref should be ignored.",
			},
			{
				FailureRef:    "f1",
				TestName:      "uploads file",
				BackendArea:   "file-storage",
				ExpectedError: "POST /api/storage returned 500 for claim-123",
				SearchTerms:   []string{"claim-123", "file-storage", "500"},
				LogQL:         `{namespace=~".+"} |~ "(?i)(file-storage|claim-123)"`,
				Reason:        "The UI upload flow failed after the backend returned a storage claim error.",
				Confidence:    "high",
			},
		}, nil
	}
	defer func() {
		runGrafanaLogQueryPlanning = previousPlanner
	}()

	var queryCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "test-session")

		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "fake-grafana-mcp"},
			})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode tool params: %v", err)
			}
			switch params.Name {
			case "list_datasources":
				writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[{"uid":"loki-dev","name":"Loki","type":"loki","isDefault":true}]}`)
			case "query_loki_logs":
				queryCount.Add(1)
				var args map[string]any
				if err := json.Unmarshal(params.Arguments, &args); err != nil {
					t.Fatalf("decode query args: %v", err)
				}
				query := args["logql"].(string)
				if !strings.Contains(query, "file-storage") || !strings.Contains(query, "claim-123") {
					t.Fatalf("planned logql was not used: %s", query)
				}
				if strings.Contains(query, "unrelated") || strings.Contains(query, "broad") {
					t.Fatalf("unknown failure ref query should not be executed: %s", query)
				}
				writeMCPToolResponse(t, writer, rpc.ID, `{"data":[{"timestamp":"1780322400000000000","line":"file storage claim claim-123 reconcile failed","labels":{"namespace":"file-storage"}}],"metadata":{"linesReturned":1,"resultsTruncated":false}}`)
			default:
				t.Fatalf("unexpected tool %s", params.Name)
			}
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
		}
	}))
	defer server.Close()

	enrichment, err := runGrafanaLogEnrichment(context.Background(), Config{
		EnableGrafanaLogs:     true,
		EnableAIAnalysis:      true,
		ClaudeToken:           "test-token",
		GrafanaURL:            "https://grafana.example.com",
		GrafanaMCPEndpoint:    server.URL + "/mcp",
		GrafanaLogStart:       "2026-06-01T13:00:00Z",
		GrafanaLogEnd:         "2026-06-01T14:00:00Z",
		GrafanaLogLimit:       5,
		GrafanaLogMaxFailures: 2,
	}, Analysis{
		Failures: []TestCase{{
			ID:      "file-upload",
			Name:    "uploads file",
			Suite:   "File Storage Management",
			Message: "POST /api/storage returned 500 for claim-123",
		}, {
			ID:      "button-style",
			Name:    "button uses the primary color",
			Suite:   "Visual checks",
			Message: "expected CSS color to match",
		}},
	})
	if err != nil {
		t.Fatalf("runGrafanaLogEnrichment returned error: %v", err)
	}
	if queryCount.Load() != 1 {
		t.Fatalf("expected one valid planned query_loki_logs call, got %d", queryCount.Load())
	}
	if enrichment == nil || len(enrichment.Contexts) != 1 {
		t.Fatalf("unexpected enrichment: %+v", enrichment)
	}
	context := enrichment.Contexts[0]
	if context.Test == nil || context.Test.Name != "uploads file" {
		t.Fatalf("planned query was not attached to the related test: %+v", context.Test)
	}
	if context.QueryLabel != "AI-planned backend query" || !strings.Contains(context.Reason, "storage claim") {
		t.Fatalf("unexpected planned query metadata: %+v", context)
	}
	if context.FailureRef != "f1" ||
		context.TestName != "uploads file" ||
		context.BackendArea != "file-storage" ||
		context.ExpectedError != "POST /api/storage returned 500 for claim-123" ||
		context.Confidence != "high" ||
		!strings.Contains(context.GrafanaExploreURL, "/explore?") {
		t.Fatalf("planned query metadata was not attached: %+v", context)
	}
	if len(context.SearchTerms) != 3 {
		t.Fatalf("search terms were not attached: %+v", context.SearchTerms)
	}
	if len(context.Entries) != 1 || !strings.Contains(context.Entries[0].Line, "claim-123") {
		t.Fatalf("unexpected log entries: %+v", context.Entries)
	}
}

func TestRunGrafanaLogEnrichmentQueriesPlannedFailuresInParallel(t *testing.T) {
	previousPlanner := runGrafanaLogQueryPlanning
	runGrafanaLogQueryPlanning = func(_ context.Context, _ Config, _ Analysis) ([]GrafanaLogPlannedQuery, error) {
		return []GrafanaLogPlannedQuery{
			{
				FailureRef: "f1",
				TestName:   "uploads file",
				LogQL:      `{namespace=~".+"} |= "claim-123"`,
				Reason:     "File upload needs backend log evidence.",
			},
			{
				FailureRef: "f2",
				TestName:   "creates instance",
				LogQL:      `{namespace=~".+"} |= "instance-456"`,
				Reason:     "Instance creation needs backend log evidence.",
			},
		}, nil
	}
	defer func() {
		runGrafanaLogQueryPlanning = previousPlanner
	}()

	queryStarted := make(chan string, 2)
	releaseQueries := make(chan struct{})
	var activeQueries atomic.Int32
	var maxActiveQueries atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "test-session")

		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "fake-grafana-mcp"},
			})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode tool params: %v", err)
			}
			switch params.Name {
			case "list_datasources":
				writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[{"uid":"loki-dev","name":"Loki","type":"loki","isDefault":true}]}`)
			case "query_loki_logs":
				var args map[string]any
				if err := json.Unmarshal(params.Arguments, &args); err != nil {
					t.Fatalf("decode query args: %v", err)
				}
				current := activeQueries.Add(1)
				for {
					maximum := maxActiveQueries.Load()
					if current <= maximum || maxActiveQueries.CompareAndSwap(maximum, current) {
						break
					}
				}
				queryStarted <- args["logql"].(string)
				<-releaseQueries
				activeQueries.Add(-1)
				writeMCPToolResponse(t, writer, rpc.ID, `{"data":[{"timestamp":"1780322400000000000","line":"parallel query result","labels":{"namespace":"test"}}],"metadata":{"linesReturned":1,"resultsTruncated":false}}`)
			default:
				t.Fatalf("unexpected tool %s", params.Name)
			}
		default:
			t.Fatalf("unexpected method %s", rpc.Method)
		}
	}))
	defer server.Close()

	type result struct {
		enrichment *GrafanaLogEnrichment
		err        error
	}
	done := make(chan result, 1)
	go func() {
		enrichment, err := runGrafanaLogEnrichment(context.Background(), Config{
			EnableGrafanaLogs:     true,
			EnableAIAnalysis:      true,
			ClaudeToken:           "test-token",
			GrafanaMCPEndpoint:    server.URL + "/mcp",
			GrafanaLogStart:       "2026-06-01T13:00:00Z",
			GrafanaLogEnd:         "2026-06-01T14:00:00Z",
			GrafanaLogLimit:       5,
			GrafanaLogMaxFailures: 2,
			GrafanaLogConcurrency: 2,
		}, Analysis{
			Failures: []TestCase{{
				ID:   "file-upload",
				Name: "uploads file",
			}, {
				ID:   "instance-create",
				Name: "creates instance",
			}},
		})
		done <- result{enrichment: enrichment, err: err}
	}()

	startedCount := 0
	for startedCount < 2 {
		select {
		case <-queryStarted:
			startedCount++
		case <-time.After(500 * time.Millisecond):
			close(releaseQueries)
			received := <-done
			if received.err != nil {
				t.Fatalf("runGrafanaLogEnrichment returned error after timeout: %v", received.err)
			}
			t.Fatalf("expected two parallel query_loki_logs calls before releasing responses, saw %d with enrichment %+v", startedCount, received.enrichment)
		}
	}
	close(releaseQueries)

	received := <-done
	if received.err != nil {
		t.Fatalf("runGrafanaLogEnrichment returned error: %v", received.err)
	}
	if received.enrichment == nil || len(received.enrichment.Contexts) != 2 {
		t.Fatalf("unexpected enrichment: %+v", received.enrichment)
	}
	if maxActiveQueries.Load() < 2 {
		t.Fatalf("queries did not overlap, max active = %d", maxActiveQueries.Load())
	}
}

func TestGrafanaExploreURL(t *testing.T) {
	t.Parallel()

	lookup := grafanaExploreURL("https://grafana.example.com/grafana?orgId=7", "99", "loki-dev", `{namespace="file-storage"} |= "claim-123"`, "2026-06-01T13:00:00Z", "2026-06-01T14:00:00Z")
	parsed, err := url.Parse(lookup)
	if err != nil {
		t.Fatalf("parse lookup URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "grafana.example.com" || parsed.Path != "/grafana/explore" {
		t.Fatalf("unexpected lookup URL: %s", lookup)
	}
	if parsed.Query().Get("orgId") != "7" || parsed.Query().Get("schemaVersion") != "1" {
		t.Fatalf("unexpected lookup query params: %s", lookup)
	}
	panes := parsed.Query().Get("panes")
	if !strings.Contains(panes, "loki-dev") || !strings.Contains(panes, "claim-123") || !strings.Contains(panes, "2026-06-01T13:00:00Z") {
		t.Fatalf("lookup panes missing expected query state: %s", panes)
	}
}

func TestSafeURLForLogRedactsSensitiveURLParts(t *testing.T) {
	t.Parallel()

	redacted := safeURLForLog("https://user:secret@grafana.example.com/mcp?token=abc#fragment")

	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "token=abc") || strings.Contains(redacted, "fragment") {
		t.Fatalf("URL was not redacted for logs: %s", redacted)
	}
	if !strings.Contains(redacted, "grafana.example.com/mcp") || !strings.Contains(redacted, "%3Credacted%3E") {
		t.Fatalf("URL lost useful endpoint context: %s", redacted)
	}
}

func TestGrafanaSelfObservabilityLogFilter(t *testing.T) {
	t.Parallel()

	if !isGrafanaSelfObservabilityLog(GrafanaLogEntry{
		Line:   `Round trip completed url:http://grafana/api/datasources/proxy/uid/loki/loki/api/v1/query_range?query=%7Bnamespace%3D~%22.%2B%22%7D`,
		Labels: map[string]string{"namespace": "grafana"},
	}) {
		t.Fatal("expected Grafana query echo log to be filtered")
	}
	if !isGrafanaSelfObservabilityLog(GrafanaLogEntry{
		Line:   `level=info ts=2026-06-01T20:59:22.189185149Z caller=metrics.go:237 component=querier org_id=fake latency=fast query="{namespace=~\".+\"} |~ \"mcp-verification-request-26761890035\"" query_hash=1493516980 query_type=filter`,
		Labels: map[string]string{"namespace": "loki"},
	}) {
		t.Fatal("expected Loki querier query metrics log to be filtered")
	}
	if isGrafanaSelfObservabilityLog(GrafanaLogEntry{
		Line:   "file-storage controller failed request_id=mcp-verification-request-26761890035 with internal_error",
		Labels: map[string]string{"namespace": "file-storage", "pod": "file-storage-api-123"},
	}) {
		t.Fatal("backend log evidence should not be filtered")
	}
}

func TestRunGrafanaLogEnrichmentSkipsMCPWhenAIPlansNoQueries(t *testing.T) {
	previousPlanner := runGrafanaLogQueryPlanning
	runGrafanaLogQueryPlanning = func(_ context.Context, _ Config, _ Analysis) ([]GrafanaLogPlannedQuery, error) {
		return nil, nil
	}
	defer func() {
		runGrafanaLogQueryPlanning = previousPlanner
	}()

	enrichment, err := runGrafanaLogEnrichment(context.Background(), Config{
		EnableGrafanaLogs: true,
		EnableAIAnalysis:  true,
		ClaudeToken:       "test-token",
	}, Analysis{
		Failures: []TestCase{{
			ID:      "visual-only",
			Name:    "button uses the primary color",
			Suite:   "Visual checks",
			Message: "expected CSS color to match",
		}},
	})
	if err != nil {
		t.Fatalf("runGrafanaLogEnrichment returned error: %v", err)
	}
	if enrichment != nil {
		t.Fatalf("expected nil enrichment when no backend log queries are planned, got %+v", enrichment)
	}
}

func writeMCPResponse(t *testing.T, writer http.ResponseWriter, id json.RawMessage, result any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	}); err != nil {
		t.Fatalf("write MCP response: %v", err)
	}
}

func writeMCPToolResponse(t *testing.T, writer http.ResponseWriter, id json.RawMessage, text string) {
	t.Helper()
	writeMCPResponse(t, writer, id, map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": text,
		}},
	})
}
