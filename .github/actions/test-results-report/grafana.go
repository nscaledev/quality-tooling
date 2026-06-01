package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const mcpProtocolVersion = "2024-11-05"

type mcpHTTPClient struct {
	endpoint   string
	httpClient *http.Client
	nextID     int
	sessionID  string
	ready      bool
}

type mcpRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *mcpRPCErrorBody `json:"error,omitempty"`
}

type mcpRPCErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

type listDatasourcesResult struct {
	Datasources []struct {
		UID       string `json:"uid"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		IsDefault bool   `json:"isDefault"`
	} `json:"datasources"`
}

type queryLokiLogsResult struct {
	Data []struct {
		Timestamp          string            `json:"timestamp,omitempty"`
		Line               string            `json:"line,omitempty"`
		Labels             map[string]string `json:"labels,omitempty"`
		StructuredMetadata map[string]string `json:"structuredMetadata,omitempty"`
		Parsed             map[string]string `json:"parsed,omitempty"`
	} `json:"data"`
	Metadata *struct {
		LinesReturned    int  `json:"linesReturned"`
		ResultsTruncated bool `json:"resultsTruncated"`
	} `json:"metadata,omitempty"`
}

func runGrafanaLogEnrichment(ctx context.Context, config Config, analysis Analysis) (*GrafanaLogEnrichment, error) {
	if !config.EnableGrafanaLogs {
		return nil, nil
	}
	if len(analysis.Failures) == 0 {
		return nil, nil
	}
	if config.GrafanaMCPEndpoint == "" {
		return nil, fmt.Errorf("grafana log enrichment is enabled but no grafana-mcp-endpoint/GRAFANA_MCP_ENDPOINT is available")
	}
	if strings.TrimSpace(config.GrafanaLogQL) == "" && strings.TrimSpace(config.GrafanaLogQLTemplate) == "" {
		return nil, fmt.Errorf("grafana log enrichment is enabled but neither grafana-logql nor grafana-logql-template was provided")
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	client := newMCPHTTPClient(config.GrafanaMCPEndpoint)
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}

	uid, name, err := resolveLokiDatasource(ctx, client, config)
	if err != nil {
		return nil, err
	}

	start, end, err := grafanaLogTimeRange(config, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	enrichment := &GrafanaLogEnrichment{
		DatasourceUID:  uid,
		DatasourceName: name,
		StartRFC3339:   start,
		EndRFC3339:     end,
	}

	if query := strings.TrimSpace(config.GrafanaLogQL); query != "" {
		enrichment.Contexts = append(enrichment.Contexts, queryGrafanaLogs(ctx, client, uid, query, start, end, config.GrafanaLogLimit, nil, "General query"))
	}

	if template := strings.TrimSpace(config.GrafanaLogQLTemplate); template != "" {
		for _, failure := range selectFailuresForGrafanaLogs(analysis, config.GrafanaLogMaxFailures) {
			test := failure
			query := renderGrafanaLogQLTemplate(template, failure, config)
			enrichment.Contexts = append(enrichment.Contexts, queryGrafanaLogs(ctx, client, uid, query, start, end, config.GrafanaLogLimit, &test, "Failure query"))
		}
	}

	return enrichment, nil
}

func newMCPHTTPClient(endpoint string) *mcpHTTPClient {
	return &mcpHTTPClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (client *mcpHTTPClient) initialize(ctx context.Context) error {
	if client.ready {
		return nil
	}

	_, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "nscale-test-results-report",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize grafana MCP client: %w", err)
	}

	if _, err := client.notification(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("send grafana MCP initialized notification: %w", err)
	}

	client.ready = true
	return nil
}

func (client *mcpHTTPClient) callTool(ctx context.Context, name string, arguments map[string]any) ([]byte, error) {
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}

	raw, err := client.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}

	var result mcpToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode MCP tool result for %s: %w", name, err)
	}
	text := strings.TrimSpace(joinMCPTextContent(result.Content))
	if result.IsError {
		if text == "" {
			text = "tool returned isError=true"
		}
		return nil, fmt.Errorf("grafana MCP tool %s failed: %s", name, text)
	}
	if text == "" {
		return raw, nil
	}
	return []byte(extractJSONText(text)), nil
}

func (client *mcpHTTPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	client.nextID++
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      client.nextID,
		"method":  method,
		"params":  params,
	}
	response, err := client.post(ctx, payload, true)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (client *mcpHTTPClient) notification(ctx context.Context, method string, params any) (json.RawMessage, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	response, err := client.post(ctx, payload, false)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, nil
	}
	return response.Result, nil
}

func (client *mcpHTTPClient) post(ctx context.Context, payload map[string]any, expectResponse bool) (*mcpRPCResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if client.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", client.sessionID)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		client.sessionID = sessionID
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP %s returned status %d: %s", payload["method"], response.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		if expectResponse {
			return nil, fmt.Errorf("MCP %s returned an empty response", payload["method"])
		}
		return nil, nil
	}

	message, err := decodeMCPResponseBody(response.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, fmt.Errorf("decode MCP %s response: %w", payload["method"], err)
	}
	if message.Error != nil {
		return nil, fmt.Errorf("MCP %s error %d: %s", payload["method"], message.Error.Code, message.Error.Message)
	}
	return message, nil
}

func decodeMCPResponseBody(contentType string, body []byte) (*mcpRPCResponse, error) {
	payload := strings.TrimSpace(string(body))
	if strings.Contains(contentType, "text/event-stream") || strings.HasPrefix(payload, "event:") || strings.HasPrefix(payload, "data:") {
		var err error
		payload, err = firstSSEDataPayload(payload)
		if err != nil {
			return nil, err
		}
	}

	var response mcpRPCResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func firstSSEDataPayload(body string) (string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	for _, event := range strings.Split(body, "\n\n") {
		var dataLines []string
		for _, line := range strings.Split(event, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		if strings.HasPrefix(payload, "{") {
			return payload, nil
		}
	}
	return "", fmt.Errorf("no JSON data payload found in SSE response")
}

func joinMCPTextContent(content []struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}) string {
	var parts []string
	for _, item := range content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractJSONText(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			return strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	return trimmed
}

func resolveLokiDatasource(ctx context.Context, client *mcpHTTPClient, config Config) (string, string, error) {
	if config.GrafanaLokiUID != "" {
		return config.GrafanaLokiUID, config.GrafanaLokiName, nil
	}

	raw, err := client.callTool(ctx, "list_datasources", map[string]any{
		"type":  "loki",
		"limit": 100,
	})
	if err != nil {
		return "", "", err
	}

	var result listDatasourcesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", "", fmt.Errorf("decode list_datasources result: %w: %s", err, string(raw))
	}

	var fallbackUID, fallbackName string
	for _, datasource := range result.Datasources {
		if fallbackUID == "" || datasource.IsDefault {
			fallbackUID = datasource.UID
			fallbackName = datasource.Name
		}
		if config.GrafanaLokiName != "" && strings.EqualFold(datasource.Name, config.GrafanaLokiName) {
			return datasource.UID, datasource.Name, nil
		}
	}

	if fallbackUID == "" {
		return "", "", fmt.Errorf("no Loki datasource was returned by Grafana MCP list_datasources")
	}
	return fallbackUID, fallbackName, nil
}

func queryGrafanaLogs(ctx context.Context, client *mcpHTTPClient, datasourceUID, logql, start, end string, limit int, test *TestCase, label string) GrafanaLogContext {
	context := GrafanaLogContext{
		Test:       test,
		Query:      logql,
		QueryLabel: label,
	}

	raw, err := client.callTool(ctx, "query_loki_logs", map[string]any{
		"datasourceUid": datasourceUID,
		"logql":         logql,
		"startRfc3339":  start,
		"endRfc3339":    end,
		"limit":         limit,
		"direction":     "backward",
		"queryType":     "range",
	})
	if err != nil {
		context.Error = err.Error()
		return context
	}

	var result queryLokiLogsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		context.Error = fmt.Sprintf("decode query_loki_logs result: %v: %s", err, string(raw))
		return context
	}

	context.LineCount = len(result.Data)
	if result.Metadata != nil {
		context.LineCount = result.Metadata.LinesReturned
		context.Truncated = result.Metadata.ResultsTruncated
	}
	for _, entry := range result.Data {
		context.Entries = append(context.Entries, GrafanaLogEntry{
			Timestamp:          entry.Timestamp,
			Line:               truncate(cleanOneLine(entry.Line), 800),
			Labels:             entry.Labels,
			StructuredMetadata: entry.StructuredMetadata,
			Parsed:             entry.Parsed,
		})
	}
	return context
}

func grafanaLogTimeRange(config Config, now time.Time) (string, string, error) {
	end := now
	if config.GrafanaLogEnd != "" {
		parsed, err := time.Parse(time.RFC3339, config.GrafanaLogEnd)
		if err != nil {
			return "", "", fmt.Errorf("parse grafana-log-end: %w", err)
		}
		end = parsed
	}

	lookback, err := time.ParseDuration(firstNonEmpty(config.GrafanaLogLookback, "1h"))
	if err != nil {
		return "", "", fmt.Errorf("parse grafana-log-lookback: %w", err)
	}

	start := end.Add(-lookback)
	if config.GrafanaLogStart != "" {
		parsed, err := time.Parse(time.RFC3339, config.GrafanaLogStart)
		if err != nil {
			return "", "", fmt.Errorf("parse grafana-log-start: %w", err)
		}
		start = parsed
	}

	return start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), nil
}

func selectFailuresForGrafanaLogs(analysis Analysis, limit int) []TestCase {
	if limit <= 0 {
		limit = 3
	}

	candidates := analysis.Failures
	if analysis.Compare != nil && len(analysis.Compare.NewFailures) > 0 {
		candidates = append([]TestCase{}, analysis.Compare.NewFailures...)
		if len(candidates) < limit {
			seen := map[string]bool{}
			for _, failure := range candidates {
				seen[failure.ID] = true
			}
			for _, failure := range analysis.Failures {
				if !seen[failure.ID] {
					candidates = append(candidates, failure)
				}
				if len(candidates) >= limit {
					break
				}
			}
		}
	}

	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func renderGrafanaLogQLTemplate(template string, test TestCase, config Config) string {
	keywords := logKeywordRegex(test)
	replacements := map[string]string{
		"test_id":            logQLStringEscape(test.ID),
		"test_name":          logQLStringEscape(test.Name),
		"test_suite":         logQLStringEscape(test.Suite),
		"test_file":          logQLStringEscape(test.File),
		"failure_message":    logQLStringEscape(test.Message),
		"environment":        logQLStringEscape(config.Environment),
		"log_keywords":       keywords,
		"log_keywords_regex": keywords,
	}

	output := template
	for key, value := range replacements {
		output = strings.ReplaceAll(output, "{{"+key+"}}", value)
	}
	return output
}

func logQLStringEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	return value
}

func logKeywordRegex(test TestCase) string {
	stopWords := map[string]bool{
		"should": true, "test": true, "with": true, "from": true, "that": true,
		"when": true, "then": true, "error": true, "failed": true, "failure": true,
		"timeout": true, "expected": true, "received": true, "status": true,
	}
	fields := []string{test.Suite, test.Name, test.File, test.Message}
	seen := map[string]bool{}
	var keywords []string
	tokenPattern := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._/-]{3,}`)
	for _, field := range fields {
		for _, token := range tokenPattern.FindAllString(field, -1) {
			token = strings.Trim(strings.ToLower(token), "._/-")
			if len(token) < 4 || stopWords[token] || seen[token] {
				continue
			}
			seen[token] = true
			keywords = append(keywords, regexp.QuoteMeta(token))
			if len(keywords) >= 8 {
				return strings.Join(keywords, "|")
			}
		}
	}
	sort.Strings(keywords)
	return strings.Join(keywords, "|")
}
