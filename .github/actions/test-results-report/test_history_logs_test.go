package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunTestHistoryLogEnrichmentQueriesSpecificFailedTests(t *testing.T) {
	t.Parallel()

	var receivedLogQL string
	var receivedDatasourceUID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		switch rpc.Method {
		case "initialize":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{"protocolVersion": mcpProtocolVersion})
		case "notifications/initialized":
			writeMCPResponse(t, writer, rpc.ID, map[string]any{})
		case "tools/call":
			switch rpc.Params.Name {
			case "list_datasources":
				writeMCPToolResponse(t, writer, rpc.ID, `{"datasources":[{"uid":"loki-dev","name":"Loki","type":"loki","isDefault":true},{"uid":"product-loki-uid","name":"product-loki","type":"loki"}]}`)
			case "query_loki_logs":
				receivedLogQL, _ = rpc.Params.Arguments["logql"].(string)
				receivedDatasourceUID, _ = rpc.Params.Arguments["datasourceUid"].(string)
				writeMCPToolResponse(t, writer, rpc.ID, `{"data":[
					{"timestamp":"1780322400000000000","line":"test_history result failed run_id=run-previous: creates network; ai_likely_reason=Network provisioning hit VLAN exhaustion; ai_next_check=Check VLAN allocations","labels":{"service_name":"test-results-report"},"structuredMetadata":{"test_history_repo":"nscale/repo","test_history_suite":"region-api","test_history_env":"dev","test_history_run_id":"run-previous","test_history_run_attempt":"2","test_history_test_id":"network::creates network","test_history_test_name":"creates network","test_history_failure_fingerprint":"sha256:match","test_history_failure_category":"infra/external"}},
					{"timestamp":"1780322500000000000","line":"test_history result failed run_id=run-current: creates network; ai_likely_reason=Current run should be filtered","labels":{"service_name":"test-results-report"},"structuredMetadata":{"test_history_repo":"nscale/repo","test_history_suite":"region-api","test_history_env":"dev","test_history_run_id":"run-current","test_history_test_id":"network::creates network","test_history_test_name":"creates network"}},
					{"timestamp":"1780322600000000000","line":"test_history result failed run_id=run-other: creates network; ai_likely_reason=Other repo should be filtered","labels":{"service_name":"test-results-report"},"structuredMetadata":{"test_history_repo":"other/repo","test_history_suite":"region-api","test_history_env":"dev","test_history_run_id":"run-other","test_history_test_id":"network::creates network","test_history_test_name":"creates network"}}
				],"metadata":{"linesReturned":3,"resultsTruncated":false}}`)
			default:
				t.Fatalf("unexpected MCP tool call: %s", rpc.Params.Name)
			}
		default:
			t.Fatalf("unexpected MCP method: %s", rpc.Method)
		}
	}))
	defer server.Close()

	enrichment, err := runTestHistoryLogEnrichment(context.Background(), Config{
		EnableTestHistoryLogs:     true,
		GrafanaMCPEndpoint:        server.URL + "/mcp",
		GrafanaLokiUID:            "loki-dev",
		GrafanaLokiName:           "Loki",
		TestHistoryLogLookback:    "24h",
		TestHistoryLogLimit:       5,
		TestHistoryLogMaxFailures: 1,
		TestHistoryRepo:           "nscale/repo",
		TestHistorySuite:          "region-api",
		TestHistoryEnv:            "dev",
		TestHistoryRunID:          "run-current",
		GrafanaLogConcurrency:     1,
	}, Analysis{
		Failures: []TestCase{{
			ID:      "network::creates network",
			Suite:   "network",
			Name:    "creates network",
			Message: "network entered error",
		}},
	})
	if err != nil {
		t.Fatalf("runTestHistoryLogEnrichment returned error: %v", err)
	}
	if enrichment == nil || len(enrichment.Contexts) != 1 {
		t.Fatalf("expected one history context, got %+v", enrichment)
	}
	context := enrichment.Contexts[0]
	if context.RawLineCount != 3 || context.LineCount != 1 || len(context.Observations) != 1 {
		t.Fatalf("unexpected history context counts: %+v", context)
	}
	if enrichment.DatasourceUID != "product-loki-uid" || enrichment.DatasourceName != "product-loki" || receivedDatasourceUID != "product-loki-uid" {
		t.Fatalf("history lookup should use product-loki datasource, enrichment=%+v query datasource=%q", enrichment, receivedDatasourceUID)
	}
	observation := context.Observations[0]
	if observation.RunID != "run-previous" || observation.AILikelyReason != "Network provisioning hit VLAN exhaustion" || observation.AINextCheck != "Check VLAN allocations" {
		t.Fatalf("unexpected history observation: %+v", observation)
	}
	for _, expected := range []string{
		`{service_name="test-results-report"}`,
		`|= "test_history result failed"`,
		`|= "creates network"`,
		`!= "run_id=run-current"`,
	} {
		if !strings.Contains(receivedLogQL, expected) {
			t.Fatalf("LogQL missing %q: %s", expected, receivedLogQL)
		}
	}
	if strings.Contains(receivedLogQL, "kubernetes") {
		t.Fatalf("history LogQL should not include kubernetes service filter: %s", receivedLogQL)
	}
}

func TestBuildTestHistoryLogQLTargetsSpecificTest(t *testing.T) {
	t.Parallel()

	logql := buildTestHistoryLogQL(`{service_name="test-results-report"}`, `creates "quoted" network`, "run-123")
	for _, expected := range []string{
		`{service_name="test-results-report"}`,
		`|= "test_history result failed"`,
		`|= "creates \"quoted\" network"`,
		`!= "run_id=run-123"`,
	} {
		if !strings.Contains(logql, expected) {
			t.Fatalf("LogQL missing %q: %s", expected, logql)
		}
	}
}

func TestBuildTestHistoryLogQLUsesCustomSelector(t *testing.T) {
	t.Parallel()

	logql := buildTestHistoryLogQL(`{service_name="test-results-report",test_history_env="dev"}`, "creates network", "")
	if !strings.HasPrefix(logql, `{service_name="test-results-report",test_history_env="dev"}`) {
		t.Fatalf("LogQL should use custom selector: %s", logql)
	}
	if strings.Contains(logql, `!= "run_id=`) {
		t.Fatalf("LogQL should not exclude a run when current run ID is empty: %s", logql)
	}
}
