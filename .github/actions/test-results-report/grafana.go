package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const mcpProtocolVersion = "2024-11-05"

func logGrafana(format string, args ...any) {
	fmt.Printf("Grafana MCP: "+format+"\n", args...)
}

type mcpHTTPClient struct {
	endpoint   string
	httpClient *http.Client
	mu         sync.Mutex
	initMu     sync.Mutex
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

type grafanaDatasource struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

type listDatasourcesResult struct {
	Datasources []grafanaDatasource `json:"datasources"`
}

type queryLokiLogsResult struct {
	Data     []queryLokiLogEntry `json:"data"`
	Metadata *queryLokiMetadata  `json:"metadata,omitempty"`
}

type queryLokiLogEntry struct {
	Timestamp          string            `json:"timestamp,omitempty"`
	Line               string            `json:"line,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	StructuredMetadata map[string]string `json:"structuredMetadata,omitempty"`
	Parsed             map[string]string `json:"parsed,omitempty"`
}

type queryLokiMetadata struct {
	LinesReturned    int  `json:"linesReturned"`
	ResultsTruncated bool `json:"resultsTruncated"`
}

type grafanaFailureCandidate struct {
	Ref  string
	Test TestCase
}

type grafanaLogQueryJob struct {
	Test          *TestCase
	FailureRef    string
	TestName      string
	BackendArea   string
	ExpectedError string
	SearchTerms   []string
	LogQL         string
	Label         string
	Reason        string
	Confidence    string
}

func runGrafanaLogEnrichment(ctx context.Context, config Config, analysis Analysis) (*GrafanaLogEnrichment, error) {
	if !config.EnableGrafanaLogs {
		return nil, nil
	}

	fmt.Println("::group::Grafana MCP log enrichment")
	defer fmt.Println("::endgroup::")

	logGrafana("enabled; failures=%d ai_analysis=%t claude_token_configured=%t endpoint_configured=%t max_failures=%d concurrency=%d lookback=%s",
		len(analysis.Failures),
		config.EnableAIAnalysis,
		config.ClaudeToken != "",
		config.GrafanaMCPEndpoint != "",
		normalizedGrafanaFailureLimit(config.GrafanaLogMaxFailures),
		config.GrafanaLogConcurrency,
		firstNonEmpty(config.GrafanaLogLookback, "1h"),
	)
	if len(analysis.Failures) == 0 {
		logGrafana("skipping lookup because there are no failed tests")
		return nil, nil
	}

	var plannedQueries []GrafanaLogPlannedQuery
	if config.GrafanaQueryPlanPath != "" {
		logGrafana("loading preplanned backend Loki query plan from %s", config.GrafanaQueryPlanPath)
		var err error
		plannedQueries, err = readGrafanaLogQueryPlan(config.GrafanaQueryPlanPath)
		if err != nil {
			return nil, err
		}
		originalPlannedCount := len(plannedQueries)
		plannedQueries = limitGrafanaPlannedQueries(plannedQueries, config.GrafanaLogMaxFailures)
		logGrafana("loaded %d preplanned backend query/queries; using %d after limit", originalPlannedCount, len(plannedQueries))
		logGrafanaPlannedQueries(plannedQueries)
	} else {
		planningAttempted := config.EnableAIAnalysis && config.ClaudeToken != ""
		if planningAttempted {
			var err error
			plannedQueries, err = planGrafanaLogQueries(ctx, config, analysis)
			if err != nil {
				return nil, err
			}
		} else {
			logGrafanaPlanningSkip(config, analysis)
			return nil, fmt.Errorf("grafana log enrichment requires enable-ai-analysis and claude-token/CLAUDE_CODE_OAUTH_TOKEN so Claude can select backend-related failures")
		}
	}
	if len(plannedQueries) == 0 {
		logGrafana("Claude did not select any backend-related failures; skipping MCP lookup")
		return nil, nil
	}
	if config.GrafanaMCPEndpoint == "" {
		logGrafana("cannot run MCP lookup because no grafana-mcp-endpoint/GRAFANA_MCP_ENDPOINT is available")
		return nil, fmt.Errorf("grafana log enrichment has backend log queries but no grafana-mcp-endpoint/GRAFANA_MCP_ENDPOINT is available")
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	logGrafana("connecting to mcp-grafana endpoint %s", safeURLForLog(config.GrafanaMCPEndpoint))
	client := newMCPHTTPClient(config.GrafanaMCPEndpoint)
	stageStarted := time.Now()
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}
	logGrafana("MCP initialize completed in %s", formatTimingDuration(time.Since(stageStarted)))

	stageStarted = time.Now()
	uid, name, err := resolveLokiDatasource(ctx, client, config)
	if err != nil {
		return nil, err
	}
	logGrafana("using Loki datasource uid=%s name=%s", firstNonEmpty(uid, "<empty>"), firstNonEmpty(name, "<empty>"))
	logGrafana("Loki datasource resolution completed in %s", formatTimingDuration(time.Since(stageStarted)))

	start, end, err := grafanaLogTimeRange(config, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	logGrafana("query time range %s to %s", start, end)

	enrichment := &GrafanaLogEnrichment{
		DatasourceUID:  uid,
		DatasourceName: name,
		StartRFC3339:   start,
		EndRFC3339:     end,
	}

	var jobs []grafanaLogQueryJob
	if len(plannedQueries) > 0 {
		candidatesByRef := grafanaFailureCandidatesByRef(analysis, config.GrafanaLogMaxFailures)
		for _, planned := range plannedQueries {
			candidate, ok := candidatesByRef[planned.FailureRef]
			if !ok {
				continue
			}
			jobs = append(jobs, grafanaLogQueryJob{
				Test:          testCasePointer(candidate),
				FailureRef:    planned.FailureRef,
				TestName:      firstNonEmpty(planned.TestName, candidate.Name, candidate.ID),
				BackendArea:   planned.BackendArea,
				ExpectedError: planned.ExpectedError,
				SearchTerms:   planned.SearchTerms,
				LogQL:         planned.LogQL,
				Label:         "AI-planned backend query",
				Reason:        planned.Reason,
				Confidence:    planned.Confidence,
			})
		}
	}

	logGrafana("prepared %d Loki query job(s)", len(jobs))
	stageStarted = time.Now()
	enrichment.Contexts = runGrafanaLogQueryJobs(ctx, client, uid, start, end, config, jobs)
	logGrafana("Loki query jobs completed in %s", formatTimingDuration(time.Since(stageStarted)))
	logGrafana("completed Grafana MCP enrichment with %d context result(s)", len(enrichment.Contexts))
	return enrichment, nil
}

func planGrafanaLogQueries(ctx context.Context, config Config, analysis Analysis) ([]GrafanaLogPlannedQuery, error) {
	logGrafana("asking Claude to plan backend Loki queries for %d candidate failure(s)", len(selectGrafanaFailureCandidates(analysis, config.GrafanaLogMaxFailures)))
	stageStarted := time.Now()
	plannedQueries, err := runGrafanaLogQueryPlanning(ctx, config, analysis)
	logGrafana("Claude query planning completed in %s", formatTimingDuration(time.Since(stageStarted)))
	if err != nil {
		return nil, err
	}
	originalPlannedCount := len(plannedQueries)
	plannedQueries = limitGrafanaPlannedQueries(plannedQueries, config.GrafanaLogMaxFailures)
	logGrafana("Claude planned %d backend query/queries; using %d after limit", originalPlannedCount, len(plannedQueries))
	logGrafanaPlannedQueries(plannedQueries)
	return plannedQueries, nil
}

func logGrafanaPlanningSkip(config Config, analysis Analysis) {
	switch {
	case !config.EnableGrafanaLogs:
		logGrafana("query planning skipped because enable-grafana-log-enrichment is false")
	case len(analysis.Failures) == 0:
		logGrafana("query planning skipped because there are no failed tests")
	case !config.EnableAIAnalysis:
		logGrafana("AI query planning skipped because enable-ai-analysis is false")
	case config.ClaudeToken == "":
		logGrafana("AI query planning skipped because claude-token/CLAUDE_CODE_OAUTH_TOKEN is not configured")
	}
}

func writeGrafanaLogQueryPlan(path string, queries []GrafanaLogPlannedQuery) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("grafana query plan path is empty")
	}
	if queries == nil {
		queries = []GrafanaLogPlannedQuery{}
	}
	data, err := json.MarshalIndent(grafanaLogQueryPlanResponse{Queries: queries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode grafana query plan: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write grafana query plan %s: %w", path, err)
	}
	return nil
}

func readGrafanaLogQueryPlan(path string) ([]GrafanaLogPlannedQuery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read grafana query plan %s: %w", path, err)
	}
	return parseGrafanaLogQueryPlan(string(data))
}

func newMCPHTTPClient(endpoint string) *mcpHTTPClient {
	return &mcpHTTPClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func logGrafanaPlannedQueries(queries []GrafanaLogPlannedQuery) {
	for index, query := range queries {
		logGrafana("planned query %d/%d: ref=%s test=%s backend=%s confidence=%s reason=%s logql=%s",
			index+1,
			len(queries),
			firstNonEmpty(query.FailureRef, "<empty>"),
			truncate(cleanOneLine(firstNonEmpty(query.TestName, "<empty>")), 120),
			firstNonEmpty(query.BackendArea, "unknown"),
			firstNonEmpty(query.Confidence, "unknown"),
			truncate(cleanOneLine(query.Reason), 240),
			truncate(cleanOneLine(query.LogQL), 500),
		)
		if query.ExpectedError != "" {
			logGrafana("planned query %d exact failure error: %s", index+1, truncate(cleanOneLine(query.ExpectedError), 300))
		}
		if len(query.SearchTerms) > 0 {
			logGrafana("planned query %d search terms: %s", index+1, strings.Join(query.SearchTerms, ", "))
		}
	}
}

func safeURLForLog(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "<invalid-url>"
	}
	if parsed.User != nil {
		parsed.User = url.User("<redacted>")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "<redacted>"
	}
	parsed.Fragment = ""
	return parsed.String()
}

func (client *mcpHTTPClient) initialize(ctx context.Context) error {
	client.initMu.Lock()
	defer client.initMu.Unlock()

	client.mu.Lock()
	ready := client.ready
	client.mu.Unlock()
	if ready {
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

	client.mu.Lock()
	client.ready = true
	client.mu.Unlock()
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
	client.mu.Lock()
	client.nextID++
	id := client.nextID
	client.mu.Unlock()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
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
	client.mu.Lock()
	sessionID := client.sessionID
	client.mu.Unlock()
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		client.mu.Lock()
		client.sessionID = sessionID
		client.mu.Unlock()
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
		logGrafana("using caller-provided Loki datasource uid=%s name=%s", config.GrafanaLokiUID, firstNonEmpty(config.GrafanaLokiName, "<empty>"))
		return config.GrafanaLokiUID, config.GrafanaLokiName, nil
	}

	logGrafana("discovering Loki datasource via MCP list_datasources name_filter=%s", firstNonEmpty(config.GrafanaLokiName, "<none>"))
	raw, err := client.callTool(ctx, "list_datasources", map[string]any{
		"type":  "loki",
		"limit": 100,
	})
	if err != nil {
		return "", "", err
	}

	datasources, err := decodeListDatasourcesResult(raw)
	if err != nil {
		return "", "", fmt.Errorf("decode list_datasources result: %w: %s", err, string(raw))
	}
	logGrafana("MCP list_datasources returned %d Loki datasource(s)", len(datasources))

	var fallbackUID, fallbackName string
	for _, datasource := range datasources {
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

func decodeListDatasourcesResult(raw []byte) ([]grafanaDatasource, error) {
	var result listDatasourcesResult
	if err := json.Unmarshal(raw, &result); err == nil && result.Datasources != nil {
		return result.Datasources, nil
	}

	var datasources []grafanaDatasource
	if err := json.Unmarshal(raw, &datasources); err != nil {
		return nil, err
	}
	return datasources, nil
}

func decodeQueryLokiLogsResult(raw []byte) (queryLokiLogsResult, error) {
	var result queryLokiLogsResult
	if err := json.Unmarshal(raw, &result); err == nil && (result.Data != nil || result.Metadata != nil) {
		return result, nil
	}

	var entries []queryLokiLogEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return queryLokiLogsResult{}, err
	}
	return queryLokiLogsResult{
		Data: entries,
		Metadata: &queryLokiMetadata{
			LinesReturned: len(entries),
		},
	}, nil
}

func runGrafanaLogQueryJobs(ctx context.Context, client *mcpHTTPClient, datasourceUID, start, end string, config Config, jobs []grafanaLogQueryJob) []GrafanaLogContext {
	if len(jobs) == 0 {
		return nil
	}

	contexts := make([]GrafanaLogContext, len(jobs))
	concurrency := normalizedGrafanaLogConcurrency(config.GrafanaLogConcurrency, len(jobs))
	logGrafana("executing %d Loki query job(s) with concurrency=%d", len(jobs), concurrency)
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

			logGrafanaQueryStart(index, len(jobs), job)
			context := queryGrafanaLogs(ctx, client, datasourceUID, job.LogQL, start, end, config.GrafanaLogLimit, job.Test, job.Label, job.Reason)
			filterGrafanaLogContextByStrongSearchTerms(&context, job)
			attachGrafanaLogQueryMetadata(&context, job, grafanaExploreURL(config.GrafanaURL, config.GrafanaOrgID, datasourceUID, job.LogQL, start, end))
			logGrafanaQueryFinish(index, len(jobs), context)
			contexts[index] = context
		}()
	}

	wg.Wait()
	return contexts
}

func logGrafanaQueryStart(index, total int, job grafanaLogQueryJob) {
	logGrafana("query job %d/%d started: label=%s ref=%s test=%s backend=%s reason=%s logql=%s",
		index+1,
		total,
		firstNonEmpty(job.Label, "<empty>"),
		firstNonEmpty(job.FailureRef, "<none>"),
		truncate(cleanOneLine(firstNonEmpty(job.TestName, "<empty>")), 120),
		firstNonEmpty(job.BackendArea, "unknown"),
		truncate(cleanOneLine(job.Reason), 240),
		truncate(cleanOneLine(job.LogQL), 500),
	)
	if job.ExpectedError != "" {
		logGrafana("query job %d exact failure error: %s", index+1, truncate(cleanOneLine(job.ExpectedError), 300))
	}
}

func logGrafanaQueryFinish(index, total int, context GrafanaLogContext) {
	if context.Error != "" {
		logGrafana("query job %d/%d failed: query_loki_logs returned error: %s", index+1, total, truncate(cleanOneLine(context.Error), 500))
		return
	}
	logGrafana("query job %d/%d %s", index+1, total, grafanaQueryFinishLogMessage(context))
	if len(context.Entries) > 0 {
		logGrafana("query job %d first log line: [%s] %s",
			index+1,
			formatLogTimestamp(context.Entries[0].Timestamp),
			truncate(cleanOneLine(context.Entries[0].Line), 300),
		)
	}
}

func grafanaQueryFinishLogMessage(context GrafanaLogContext) string {
	rawLineCount := context.RawLineCount
	if rawLineCount == 0 && context.LineCount > 0 {
		rawLineCount = context.LineCount + context.FilteredLineCount
	}
	usableLineCount := len(context.Entries)
	if usableLineCount == 0 && context.LineCount > 0 && context.FilteredLineCount == 0 {
		usableLineCount = context.LineCount
	}

	status := "succeeded"
	if rawLineCount == 0 {
		status = "succeeded; Loki returned no matching log lines"
	} else if usableLineCount == 0 && context.FilteredLineCount > 0 {
		status = "succeeded; Loki returned only Grafana/MCP self-observability lines"
	} else if usableLineCount == 0 {
		status = "succeeded; Loki returned no usable log lines after filtering"
	} else {
		status = "succeeded; Loki returned usable log lines"
	}

	return fmt.Sprintf("%s: raw_lines=%d usable_lines=%d filtered_self_observability=%d truncated=%t grafana_url=%t",
		status,
		rawLineCount,
		usableLineCount,
		context.FilteredLineCount,
		context.Truncated,
		context.GrafanaExploreURL != "",
	)
}

func attachGrafanaLogQueryMetadata(context *GrafanaLogContext, job grafanaLogQueryJob, exploreURL string) {
	context.FailureRef = job.FailureRef
	context.TestName = job.TestName
	if context.TestName == "" && job.Test != nil {
		context.TestName = firstNonEmpty(job.Test.Name, job.Test.ID)
	}
	context.BackendArea = job.BackendArea
	context.ExpectedError = job.ExpectedError
	context.SearchTerms = append([]string{}, job.SearchTerms...)
	context.Confidence = job.Confidence
	context.GrafanaExploreURL = exploreURL
}

func normalizedGrafanaLogConcurrency(configured, total int) int {
	if total <= 0 {
		return 0
	}
	if configured <= 0 {
		configured = 4
	}
	if configured > total {
		return total
	}
	return configured
}

func testCasePointer(test TestCase) *TestCase {
	copy := test
	return &copy
}

func grafanaExploreURL(baseURL, orgID, datasourceUID, logql, start, end string) string {
	baseURL = strings.TrimSpace(baseURL)
	orgID = firstNonEmpty(strings.TrimSpace(orgID), "1")
	datasourceUID = strings.TrimSpace(datasourceUID)
	logql = strings.TrimSpace(logql)
	if baseURL == "" || datasourceUID == "" || logql == "" {
		return ""
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/explore"
	parsed.Fragment = ""

	query := parsed.Query()
	if query.Get("orgId") == "" {
		query.Set("orgId", orgID)
	}
	query.Set("schemaVersion", "1")

	panes := map[string]any{
		"test-results-report": map[string]any{
			"datasource": datasourceUID,
			"queries": []map[string]any{{
				"refId":     "A",
				"expr":      logql,
				"queryType": "range",
				"datasource": map[string]string{
					"type": "loki",
					"uid":  datasourceUID,
				},
			}},
			"range": map[string]string{
				"from": firstNonEmpty(start, "now-1h"),
				"to":   firstNonEmpty(end, "now"),
			},
		},
	}
	data, err := json.Marshal(panes)
	if err != nil {
		return ""
	}
	query.Set("panes", string(data))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func queryGrafanaLogs(ctx context.Context, client *mcpHTTPClient, datasourceUID, logql, start, end string, limit int, test *TestCase, label, reason string) GrafanaLogContext {
	context := GrafanaLogContext{
		Test:       test,
		Query:      logql,
		QueryLabel: label,
		Reason:     reason,
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

	result, err := decodeQueryLokiLogsResult(raw)
	if err != nil {
		context.Error = fmt.Sprintf("decode query_loki_logs result: %v: %s", err, string(raw))
		return context
	}

	context.RawLineCount = len(result.Data)
	if result.Metadata != nil {
		context.RawLineCount = result.Metadata.LinesReturned
		context.Truncated = result.Metadata.ResultsTruncated
	}
	for _, entry := range result.Data {
		logEntry := GrafanaLogEntry{
			Timestamp:          entry.Timestamp,
			Line:               truncate(cleanOneLine(entry.Line), 800),
			Labels:             entry.Labels,
			StructuredMetadata: entry.StructuredMetadata,
			Parsed:             entry.Parsed,
		}
		if isGrafanaSelfObservabilityLog(logEntry) {
			context.FilteredLineCount++
			continue
		}
		context.Entries = append(context.Entries, logEntry)
	}
	context.LineCount = len(context.Entries)
	return context
}

func filterGrafanaLogContextByStrongSearchTerms(context *GrafanaLogContext, job grafanaLogQueryJob) {
	if context.Error != "" || len(context.Entries) == 0 {
		return
	}

	strongTerms := grafanaStrongSearchTerms(job)
	if len(strongTerms) == 0 {
		return
	}

	filtered := context.Entries[:0]
	for _, entry := range context.Entries {
		if grafanaLogEntryContainsAnyTerm(entry, strongTerms) {
			filtered = append(filtered, entry)
		}
	}
	context.Entries = filtered
	context.LineCount = len(context.Entries)
}

func grafanaStrongSearchTerms(job grafanaLogQueryJob) []string {
	evidence := grafanaFailureEvidenceForSearchTerms(job)
	seen := map[string]bool{}
	var terms []string
	for _, term := range job.SearchTerms {
		term = strings.TrimSpace(term)
		if !isStrongGrafanaSearchTerm(term, evidence) {
			continue
		}
		key := strings.ToLower(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, term)
	}
	return terms
}

func grafanaFailureEvidenceForSearchTerms(job grafanaLogQueryJob) string {
	fields := []string{job.ExpectedError}
	if job.Test != nil {
		fields = append(fields, job.Test.Message, job.Test.Output)
	}
	return strings.ToLower(strings.Join(fields, "\n"))
}

func isStrongGrafanaSearchTerm(term string, lowerEvidence string) bool {
	term = strings.TrimSpace(term)
	if term == "" || isGenericGrafanaSearchTerm(term) || isScopeOnlyGrafanaSearchTerm(term, lowerEvidence) {
		return false
	}
	if !strings.Contains(lowerEvidence, strings.ToLower(term)) {
		return false
	}
	if regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(term) {
		return true
	}
	if regexp.MustCompile(`(?i)^[0-9a-f]{16,32}$`).MatchString(term) {
		return true
	}
	return len(term) >= 12 &&
		regexp.MustCompile(`[0-9]`).MatchString(term) &&
		strings.ContainsAny(term, "-_./")
}

func isGenericGrafanaSearchTerm(term string) bool {
	lower := strings.ToLower(strings.Trim(term, ` "'`))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "/api/") {
		return true
	}
	if regexp.MustCompile(`^[45][0-9]{2}$`).MatchString(lower) {
		return true
	}
	if regexp.MustCompile(`^[a-z]{2,}-[a-z]+[0-9]+$`).MatchString(lower) {
		return true
	}
	switch lower {
	case "error", "failed", "failure", "timeout", "provisioned", "provisioning", "network", "networks", "instance", "instances":
		return true
	default:
		return false
	}
}

func isScopeOnlyGrafanaSearchTerm(term string, lowerEvidence string) bool {
	lowerTerm := strings.ToLower(strings.TrimSpace(term))
	if lowerTerm == "" || lowerEvidence == "" {
		return false
	}
	for _, marker := range []string{
		"organizationid=" + lowerTerm,
		"organization_id=" + lowerTerm,
		"organization-id=" + lowerTerm,
		"/organizations/" + lowerTerm,
		"projectid=" + lowerTerm,
		"project_id=" + lowerTerm,
		"project-id=" + lowerTerm,
		"/projects/" + lowerTerm,
		"regionid=" + lowerTerm,
		"region_id=" + lowerTerm,
		"region-id=" + lowerTerm,
	} {
		if strings.Contains(lowerEvidence, marker) {
			return true
		}
	}
	return false
}

func grafanaLogEntryContainsAnyTerm(entry GrafanaLogEntry, terms []string) bool {
	var fields []string
	fields = append(fields, entry.Line)
	for _, values := range []map[string]string{entry.Labels, entry.StructuredMetadata, entry.Parsed} {
		for _, value := range values {
			fields = append(fields, value)
		}
	}
	text := strings.ToLower(strings.Join(fields, "\n"))
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func isGrafanaSelfObservabilityLog(entry GrafanaLogEntry) bool {
	line := strings.ToLower(entry.Line)
	if strings.Contains(line, "/loki/api/v1/query_range") ||
		strings.Contains(line, "/api/datasources/proxy/") ||
		strings.Contains(line, "query_range?") {
		return true
	}
	if (strings.Contains(line, "component=querier") ||
		strings.Contains(line, "component=query-frontend") ||
		strings.Contains(line, "caller=metrics.go")) &&
		(strings.Contains(line, " query=") ||
			strings.Contains(line, " query_hash=") ||
			strings.Contains(line, " query_type=")) {
		return true
	}

	namespace := strings.ToLower(entry.Labels["namespace"])
	pod := strings.ToLower(entry.Labels["pod"])
	container := strings.ToLower(entry.Labels["container"])
	isObservabilityComponent := strings.Contains(namespace, "grafana") ||
		strings.Contains(namespace, "loki") ||
		strings.Contains(pod, "grafana") ||
		strings.Contains(pod, "loki") ||
		strings.Contains(container, "grafana") ||
		strings.Contains(container, "loki") ||
		strings.Contains(line, "mcp-grafana")
	return isObservabilityComponent &&
		(strings.Contains(line, "query=") ||
			strings.Contains(line, "logql") ||
			strings.Contains(line, "query_loki_logs") ||
			strings.Contains(line, "query_hash="))
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
	limit = normalizedGrafanaFailureLimit(limit)

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

func normalizedGrafanaFailureLimit(limit int) int {
	if limit <= 0 {
		return 3
	}
	return limit
}

func selectGrafanaFailureCandidates(analysis Analysis, limit int) []grafanaFailureCandidate {
	failures := selectFailuresForGrafanaLogs(analysis, limit)
	candidates := make([]grafanaFailureCandidate, 0, len(failures))
	for index, failure := range failures {
		candidates = append(candidates, grafanaFailureCandidate{
			Ref:  fmt.Sprintf("f%d", index+1),
			Test: failure,
		})
	}
	return candidates
}

func grafanaFailureCandidatesByRef(analysis Analysis, limit int) map[string]TestCase {
	result := map[string]TestCase{}
	for _, candidate := range selectGrafanaFailureCandidates(analysis, limit) {
		result[candidate.Ref] = candidate.Test
	}
	return result
}

func limitGrafanaPlannedQueries(queries []GrafanaLogPlannedQuery, limit int) []GrafanaLogPlannedQuery {
	limit = normalizedGrafanaFailureLimit(limit)
	if len(queries) > limit {
		return queries[:limit]
	}
	return queries
}

func logKeywordRegex(test TestCase) string {
	stopWords := map[string]bool{
		"should": true, "test": true, "with": true, "from": true, "that": true,
		"when": true, "then": true, "error": true, "failed": true, "failure": true,
		"timeout": true, "expected": true, "received": true, "status": true,
	}
	seen := map[string]bool{}
	var keywords []string
	addKeyword := func(token string) bool {
		token = strings.Trim(strings.ToLower(token), "._/-")
		if len(token) < 4 || stopWords[token] || seen[token] {
			return false
		}
		seen[token] = true
		keywords = append(keywords, regexp.QuoteMeta(token))
		return len(keywords) >= 8
	}

	identifierPattern := regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}|[0-9A-Fa-f]{16,32}`)
	for _, field := range []string{test.Message, test.Output} {
		for _, token := range identifierPattern.FindAllString(field, -1) {
			if addKeyword(token) {
				return strings.Join(keywords, "|")
			}
		}
	}

	fields := []string{test.Suite, test.Name, test.File, test.Message}
	tokenPattern := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._/-]{3,}`)
	for _, field := range fields {
		for _, token := range tokenPattern.FindAllString(field, -1) {
			if addKeyword(token) {
				return strings.Join(keywords, "|")
			}
		}
	}
	sort.Strings(keywords)
	return strings.Join(keywords, "|")
}
