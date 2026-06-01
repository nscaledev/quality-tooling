package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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
