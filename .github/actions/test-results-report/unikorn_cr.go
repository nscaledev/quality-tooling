package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type unikornCRFailureCandidate struct {
	Ref  string
	Test TestCase
}

var runKubectlGet = runKubectlGetCommand

func logUnikornCR(format string, args ...any) {
	fmt.Printf("Unikorn CR: "+format+"\n", args...)
}

func runUnikornCRPlanningMode(ctx context.Context, config Config) error {
	totalStarted := time.Now()
	if err := config.validate(); err != nil {
		return err
	}

	fmt.Println("::group::Unikorn CR query planning")
	defer fmt.Println("::endgroup::")

	analysis, err := buildAnalysis(config)
	if err != nil {
		return err
	}

	planPath := firstNonEmpty(config.UnikornCRPlanPath, filepath.Join(os.TempDir(), "test-results-report-unikorn-cr-plan.json"))
	plannedQueries := []UnikornCRPlannedQuery{}
	if config.EnableUnikornCRs && len(analysis.Failures) > 0 && config.EnableAIAnalysis && config.ClaudeToken != "" {
		plannedQueries, err = planUnikornCRQueries(ctx, config, analysis)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Unikorn CR query planning failed; kubectl setup will be skipped: %v\n", err)
			plannedQueries = []UnikornCRPlannedQuery{}
		}
	} else {
		logUnikornCRPlanningSkip(config, analysis)
	}

	if err := writeUnikornCRQueryPlan(planPath, plannedQueries); err != nil {
		return err
	}
	if err := writeUnikornCRPlanOutputs(os.Getenv("GITHUB_OUTPUT"), planPath, len(plannedQueries)); err != nil {
		return err
	}
	logUnikornCR("query plan file: %s", planPath)
	logUnikornCR("query planning output: queries=%d needs_kube=%t", len(plannedQueries), len(plannedQueries) > 0)
	logReportTiming("unikorn-cr-query-planning-total", totalStarted)
	return nil
}

func runUnikornCRCollectionMode(ctx context.Context, config Config) error {
	totalStarted := time.Now()
	fmt.Println("::group::Unikorn CR context collection")
	defer fmt.Println("::endgroup::")

	planPath := firstNonEmpty(config.UnikornCRPlanPath, filepath.Join(os.TempDir(), "test-results-report-unikorn-cr-plan.json"))
	contextPath := firstNonEmpty(config.UnikornCRContextPath, filepath.Join(os.TempDir(), "test-results-report-unikorn-cr-context.json"))

	plannedQueries, err := readUnikornCRQueryPlan(planPath)
	if err != nil {
		return err
	}
	plannedQueries = limitUnikornCRPlannedQueries(plannedQueries, config.UnikornCRMaxFailures)

	enrichment := collectUnikornCRContexts(ctx, config, plannedQueries)
	if err := writeUnikornCRContext(contextPath, enrichment); err != nil {
		return err
	}
	if err := writeUnikornCRContextOutputs(os.Getenv("GITHUB_OUTPUT"), contextPath); err != nil {
		return err
	}

	logUnikornCR("context file: %s", contextPath)
	logUnikornCR("collected %d CR context result(s)", len(enrichment.Contexts))
	logReportTiming("unikorn-cr-collection-total", totalStarted)
	return nil
}

func runUnikornCREnrichment(config Config, analysis Analysis) (*UnikornCREnrichment, error) {
	if !config.EnableUnikornCRs {
		return nil, nil
	}
	if strings.TrimSpace(config.UnikornCRContextPath) == "" {
		logUnikornCR("context path is empty; skipping CR observations")
		return nil, nil
	}

	enrichment, err := readUnikornCRContext(config.UnikornCRContextPath)
	if err != nil {
		return nil, err
	}
	if len(enrichment.Contexts) == 0 {
		return nil, nil
	}

	candidatesByRef := unikornCRFailureCandidatesByRef(analysis, config.UnikornCRMaxFailures)
	for index := range enrichment.Contexts {
		context := &enrichment.Contexts[index]
		if candidate, ok := candidatesByRef[context.FailureRef]; ok {
			context.Test = testCasePointer(candidate)
			context.TestName = firstNonEmpty(context.TestName, candidate.Name, candidate.ID)
		}
	}
	logUnikornCR("loaded %d CR observation(s) from %s", len(enrichment.Contexts), config.UnikornCRContextPath)
	return enrichment, nil
}

func planUnikornCRQueries(ctx context.Context, config Config, analysis Analysis) ([]UnikornCRPlannedQuery, error) {
	logUnikornCR("asking Claude to plan Kubernetes CR lookups for %d candidate failure(s)", len(selectUnikornCRFailureCandidates(analysis, config.UnikornCRMaxFailures)))
	stageStarted := time.Now()
	plannedQueries, err := runUnikornCRQueryPlanning(ctx, config, analysis)
	logUnikornCR("Claude CR query planning completed in %s", formatTimingDuration(time.Since(stageStarted)))
	if err != nil {
		return nil, err
	}
	originalPlannedCount := len(plannedQueries)
	plannedQueries = limitUnikornCRPlannedQueries(plannedQueries, config.UnikornCRMaxFailures)
	plannedQueries = filterUnsupportedUnikornCRPlannedQueries(plannedQueries)
	logUnikornCR("Claude planned %d CR lookup(s); using %d after limit", originalPlannedCount, len(plannedQueries))
	logUnikornCRPlannedQueries(plannedQueries)
	return plannedQueries, nil
}

func logUnikornCRPlanningSkip(config Config, analysis Analysis) {
	switch {
	case !config.EnableUnikornCRs:
		logUnikornCR("query planning skipped because enable-unikorn-cr-enrichment is false")
	case len(analysis.Failures) == 0:
		logUnikornCR("query planning skipped because there are no failed tests")
	case !config.EnableAIAnalysis:
		logUnikornCR("AI query planning skipped because enable-ai-analysis is false")
	case config.ClaudeToken == "":
		logUnikornCR("AI query planning skipped because claude-token/CLAUDE_CODE_OAUTH_TOKEN is not configured")
	}
}

func writeUnikornCRQueryPlan(path string, queries []UnikornCRPlannedQuery) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("unikorn CR query plan path is empty")
	}
	if queries == nil {
		queries = []UnikornCRPlannedQuery{}
	}
	data, err := json.MarshalIndent(unikornCRQueryPlanResponse{Queries: queries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode unikorn CR query plan: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write unikorn CR query plan %s: %w", path, err)
	}
	return nil
}

func readUnikornCRQueryPlan(path string) ([]UnikornCRPlannedQuery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read unikorn CR query plan %s: %w", path, err)
	}
	return parseUnikornCRQueryPlan(string(data))
}

func writeUnikornCRContext(path string, enrichment *UnikornCREnrichment) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("unikorn CR context path is empty")
	}
	if enrichment == nil {
		enrichment = &UnikornCREnrichment{}
	}
	if enrichment.Contexts == nil {
		enrichment.Contexts = []UnikornCRContext{}
	}
	data, err := json.MarshalIndent(enrichment, "", "  ")
	if err != nil {
		return fmt.Errorf("encode unikorn CR context: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write unikorn CR context %s: %w", path, err)
	}
	return nil
}

func readUnikornCRContext(path string) (*UnikornCREnrichment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read unikorn CR context %s: %w", path, err)
	}
	var enrichment UnikornCREnrichment
	if err := json.Unmarshal(data, &enrichment); err != nil {
		return nil, fmt.Errorf("decode unikorn CR context %s: %w", path, err)
	}
	return &enrichment, nil
}

func writeUnikornCRContextOutputs(path string, contextPath string) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "context-path=%s\n", contextPath); err != nil {
		return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
	}
	return nil
}

func collectUnikornCRContexts(ctx context.Context, config Config, queries []UnikornCRPlannedQuery) *UnikornCREnrichment {
	enrichment := &UnikornCREnrichment{Contexts: []UnikornCRContext{}}
	for _, query := range queries {
		if isUnsupportedUnikornCRResource(query.Resource) {
			logUnikornCR("skipping unsupported CR lookup ref=%s resource=%s", firstNonEmpty(strings.TrimSpace(query.FailureRef), "<empty>"), strings.TrimSpace(query.Resource))
			continue
		}
		context := UnikornCRContext{
			FailureRef:  query.FailureRef,
			TestName:    query.TestName,
			BackendArea: query.BackendArea,
			Resource:    query.Resource,
			Namespace:   query.Namespace,
			Name:        query.Name,
			Selector:    query.Selector,
			Reason:      query.Reason,
			Confidence:  query.Confidence,
		}
		if err := sanitizeUnikornCRPlannedQuery(&query); err != nil {
			context.Error = err.Error()
			enrichment.Contexts = append(enrichment.Contexts, context)
			continue
		}

		stageStarted := time.Now()
		raw, err := queryUnikornCR(ctx, config, query)
		logUnikornCR("kubectl lookup ref=%s resource=%s completed in %s", firstNonEmpty(query.FailureRef, "<empty>"), query.Resource, formatTimingDuration(time.Since(stageStarted)))
		if err != nil {
			context.Error = err.Error()
			enrichment.Contexts = append(enrichment.Contexts, context)
			continue
		}

		resultCount, objects, err := summarizeUnikornCRJSON(raw, query)
		if err != nil {
			context.Error = err.Error()
			enrichment.Contexts = append(enrichment.Contexts, context)
			continue
		}
		context.ResultCount = resultCount
		context.Objects = objects
		enrichment.Contexts = append(enrichment.Contexts, context)
	}
	return enrichment
}

func queryUnikornCR(ctx context.Context, config Config, query UnikornCRPlannedQuery) ([]byte, error) {
	timeout := config.UnikornCRTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := kubectlArgsForUnikornCR(query)
	raw, err := runKubectlGet(queryCtx, args...)
	if err == nil {
		return raw, nil
	}
	if query.AllNamespaces && query.Namespace == "" {
		fallback := query
		fallback.AllNamespaces = false
		fallbackArgs := kubectlArgsForUnikornCR(fallback)
		if fallbackRaw, fallbackErr := runKubectlGet(queryCtx, fallbackArgs...); fallbackErr == nil {
			return fallbackRaw, nil
		}
	}
	return nil, err
}

func kubectlArgsForUnikornCR(query UnikornCRPlannedQuery) []string {
	args := []string{"get", query.Resource}
	if query.Namespace != "" {
		args = append(args, "-n", query.Namespace)
		if query.Name != "" {
			args = append(args, query.Name)
		}
	} else if query.AllNamespaces {
		args = append(args, "-A")
		if query.Name != "" {
			args = append(args, "--field-selector", "metadata.name="+query.Name)
		}
	} else if query.Name != "" {
		args = append(args, query.Name)
	}
	if query.Selector != "" {
		args = append(args, "-l", query.Selector)
	}
	args = append(args, "-o", "json")
	return args
}

func runKubectlGetCommand(ctx context.Context, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl is not installed: %w", err)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl %s failed: %w: %s", strings.Join(args, " "), err, truncate(cleanOneLine(string(output)), 500))
	}
	return output, nil
}

func summarizeUnikornCRJSON(raw []byte, query UnikornCRPlannedQuery) (int, []UnikornCRObjectSummary, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return 0, nil, fmt.Errorf("decode kubectl JSON output for %s: %w", query.Resource, err)
	}

	var items []any
	if rawItems, ok := document["items"].([]any); ok {
		items = rawItems
	} else {
		items = []any{document}
	}

	var objects []UnikornCRObjectSummary
	resultCount := 0
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metadata := objectMap(object, "metadata")
		if query.Name != "" && stringField(metadata, "name") != query.Name {
			continue
		}
		if query.Namespace != "" && stringField(metadata, "namespace") != "" && stringField(metadata, "namespace") != query.Namespace {
			continue
		}
		resultCount++
		if len(objects) < 4 {
			objects = append(objects, summarizeUnikornCRObject(object))
		}
	}
	return resultCount, objects, nil
}

func summarizeUnikornCRObject(object map[string]any) UnikornCRObjectSummary {
	metadata := objectMap(object, "metadata")
	status := objectMap(object, "status")
	return UnikornCRObjectSummary{
		APIVersion:        stringField(object, "apiVersion"),
		Kind:              stringField(object, "kind"),
		Namespace:         stringField(metadata, "namespace"),
		Name:              stringField(metadata, "name"),
		Phase:             firstNonEmpty(stringField(status, "phase"), stringField(status, "status")),
		State:             stringField(status, "state"),
		ProvisioningState: firstNonEmpty(stringField(status, "provisioningStatus"), stringField(status, "provisioningState")),
		Health:            firstNonEmpty(stringField(status, "health"), stringField(status, "healthStatus")),
		DeletionTimestamp: stringField(metadata, "deletionTimestamp"),
		Conditions:        summarizeUnikornCRConditions(status),
		OwnerRefs:         summarizeUnikornCROwnerRefs(metadata),
	}
}

func summarizeUnikornCRConditions(status map[string]any) []string {
	rawConditions, ok := status["conditions"].([]any)
	if !ok {
		return nil
	}
	var conditions []string
	for _, rawCondition := range rawConditions {
		condition, ok := rawCondition.(map[string]any)
		if !ok {
			continue
		}
		parts := []string{}
		if value := stringField(condition, "type"); value != "" {
			parts = append(parts, value)
		}
		if value := stringField(condition, "status"); value != "" {
			parts = append(parts, "="+value)
		}
		if value := stringField(condition, "reason"); value != "" {
			parts = append(parts, "reason="+value)
		}
		if value := stringField(condition, "message"); value != "" {
			parts = append(parts, "message="+truncate(cleanOneLine(value), 160))
		}
		if len(parts) > 0 {
			conditions = append(conditions, strings.Join(parts, " "))
		}
		if len(conditions) >= 5 {
			break
		}
	}
	return conditions
}

func summarizeUnikornCROwnerRefs(metadata map[string]any) []string {
	rawOwners, ok := metadata["ownerReferences"].([]any)
	if !ok {
		return nil
	}
	var owners []string
	for _, rawOwner := range rawOwners {
		owner, ok := rawOwner.(map[string]any)
		if !ok {
			continue
		}
		label := strings.Trim(strings.Join([]string{stringField(owner, "kind"), stringField(owner, "name")}, "/"), "/")
		if label != "" {
			owners = append(owners, label)
		}
		if len(owners) >= 4 {
			break
		}
	}
	return owners
}

func unikornCRSignalSummary(context UnikornCRContext) string {
	if len(context.Objects) == 0 {
		return ""
	}
	var signals []string
	for _, object := range context.Objects {
		identity := strings.Trim(strings.Join([]string{object.Kind, object.Name}, "/"), "/")
		if identity == "" {
			identity = firstNonEmpty(object.Name, object.Kind, "object")
		}
		var parts []string
		if object.Namespace != "" {
			parts = append(parts, "namespace="+object.Namespace)
		}
		if object.Phase != "" {
			parts = append(parts, "phase="+object.Phase)
		}
		if object.State != "" {
			parts = append(parts, "state="+object.State)
		}
		if object.ProvisioningState != "" {
			parts = append(parts, "provisioning="+object.ProvisioningState)
		}
		if object.Health != "" {
			parts = append(parts, "health="+object.Health)
		}
		if object.DeletionTimestamp != "" {
			parts = append(parts, "deletionTimestamp="+object.DeletionTimestamp)
		}
		if len(object.Conditions) > 0 {
			parts = append(parts, "conditions="+strings.Join(object.Conditions[:min(len(object.Conditions), 2)], "; "))
		}
		if len(object.OwnerRefs) > 0 {
			parts = append(parts, "owners="+strings.Join(object.OwnerRefs, ", "))
		}
		if len(parts) == 0 {
			parts = append(parts, "no status fields found")
		}
		signals = append(signals, fmt.Sprintf("%s %s", identity, strings.Join(parts, ", ")))
		if len(signals) >= 3 {
			break
		}
	}
	return truncate(cleanOneLine(strings.Join(signals, "; ")), 500)
}

func sanitizeUnikornCRPlannedQuery(query *UnikornCRPlannedQuery) error {
	query.FailureRef = strings.TrimSpace(query.FailureRef)
	query.TestName = strings.TrimSpace(query.TestName)
	query.BackendArea = strings.TrimSpace(query.BackendArea)
	query.Resource = strings.TrimSpace(query.Resource)
	query.Namespace = strings.TrimSpace(query.Namespace)
	query.Name = strings.TrimSpace(query.Name)
	query.Selector = strings.TrimSpace(query.Selector)
	query.Reason = strings.TrimSpace(query.Reason)
	query.Confidence = normalizeGrafanaConfidence(query.Confidence)

	if query.FailureRef == "" {
		return fmt.Errorf("missing failure_ref")
	}
	if query.Resource == "" {
		return fmt.Errorf("missing resource")
	}
	if !safeUnikornCRResource.MatchString(query.Resource) {
		return fmt.Errorf("resource contains unsupported characters")
	}
	if isUnsupportedUnikornCRResource(query.Resource) {
		return fmt.Errorf("resource %q is not currently supported for CR enrichment", query.Resource)
	}
	if isBlockedUnikornCRResource(query.Resource) {
		return fmt.Errorf("resource %q is not allowed for CR enrichment", query.Resource)
	}
	if query.Namespace != "" && !safeUnikornCRName.MatchString(query.Namespace) {
		return fmt.Errorf("namespace contains unsupported characters")
	}
	if query.Name != "" && !safeUnikornCRName.MatchString(query.Name) {
		return fmt.Errorf("name contains unsupported characters")
	}
	if query.Selector != "" && !safeUnikornCRSelector.MatchString(query.Selector) {
		return fmt.Errorf("selector contains unsupported characters")
	}
	if query.Name == "" && query.Selector == "" {
		return fmt.Errorf("query must include name or selector")
	}
	if query.Namespace == "" {
		query.AllNamespaces = true
	}
	return nil
}

var (
	safeUnikornCRResource = regexp.MustCompile(`^[a-z0-9][a-z0-9./-]{0,180}$`)
	safeUnikornCRName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,180}$`)
	safeUnikornCRSelector = regexp.MustCompile(`^[A-Za-z0-9_.:/=-]+(,[A-Za-z0-9_.:/=-]+)*$`)
)

func isBlockedUnikornCRResource(resource string) bool {
	base := normalizedUnikornCRResourceBase(resource)
	switch base {
	case "pod", "secret", "configmap", "event":
		return true
	default:
		return false
	}
}

func isUnsupportedUnikornCRResource(resource string) bool {
	switch normalizedUnikornCRResourceBase(resource) {
	case "loadbalancer":
		return true
	default:
		return false
	}
}

func normalizedUnikornCRResourceBase(resource string) string {
	base := strings.ToLower(strings.Split(strings.TrimSpace(resource), ".")[0])
	return strings.TrimSuffix(base, "s")
}

func normalizedUnikornCRFailureLimit(limit int) int {
	if limit <= 0 {
		return 3
	}
	return limit
}

func selectUnikornCRFailureCandidates(analysis Analysis, limit int) []unikornCRFailureCandidate {
	failures := selectFailuresForGrafanaLogs(analysis, limit)
	candidates := make([]unikornCRFailureCandidate, 0, len(failures))
	for index, failure := range failures {
		candidates = append(candidates, unikornCRFailureCandidate{
			Ref:  fmt.Sprintf("f%d", index+1),
			Test: failure,
		})
	}
	return candidates
}

func unikornCRFailureCandidatesByRef(analysis Analysis, limit int) map[string]TestCase {
	result := map[string]TestCase{}
	for _, candidate := range selectUnikornCRFailureCandidates(analysis, limit) {
		result[candidate.Ref] = candidate.Test
	}
	return result
}

func limitUnikornCRPlannedQueries(queries []UnikornCRPlannedQuery, limit int) []UnikornCRPlannedQuery {
	limit = normalizedUnikornCRFailureLimit(limit)
	if len(queries) > limit {
		return queries[:limit]
	}
	return queries
}

func filterUnsupportedUnikornCRPlannedQueries(queries []UnikornCRPlannedQuery) []UnikornCRPlannedQuery {
	if len(queries) == 0 {
		return queries
	}
	filtered := make([]UnikornCRPlannedQuery, 0, len(queries))
	for _, query := range queries {
		if isUnsupportedUnikornCRResource(query.Resource) {
			logUnikornCR("skipping unsupported planned lookup ref=%s resource=%s", firstNonEmpty(strings.TrimSpace(query.FailureRef), "<empty>"), strings.TrimSpace(query.Resource))
			continue
		}
		filtered = append(filtered, query)
	}
	return filtered
}

func logUnikornCRPlannedQueries(queries []UnikornCRPlannedQuery) {
	for index, query := range queries {
		logUnikornCR("planned lookup %d/%d: ref=%s test=%s backend=%s resource=%s namespace=%s name=%s selector=%t all_namespaces=%t confidence=%s reason=%s",
			index+1,
			len(queries),
			firstNonEmpty(query.FailureRef, "<empty>"),
			truncate(cleanOneLine(firstNonEmpty(query.TestName, "<empty>")), 120),
			firstNonEmpty(query.BackendArea, "unknown"),
			query.Resource,
			firstNonEmpty(query.Namespace, "<empty>"),
			firstNonEmpty(query.Name, "<empty>"),
			query.Selector != "",
			query.AllNamespaces,
			firstNonEmpty(query.Confidence, "unknown"),
			truncate(cleanOneLine(query.Reason), 240),
		)
	}
}

func objectMap(parent map[string]any, key string) map[string]any {
	value, _ := parent[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func stringField(parent map[string]any, key string) string {
	switch value := parent[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}
