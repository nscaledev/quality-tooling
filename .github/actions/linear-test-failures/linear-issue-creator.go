package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nscale/quality-tooling/linear-test-failures/shared"
)

const (
	linearAPIEndpoint  = "https://api.linear.app/graphql"
	defaultPriority    = 3 // Medium
	defaultMaxFailures = 5
)

// LinearGraphQLRequest represents a GraphQL request to Linear.
type LinearGraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

// LinearGraphQLResponse represents a GraphQL response from Linear.
type LinearGraphQLResponse struct {
	Data   json.RawMessage      `json:"data"`
	Errors []LinearGraphQLError `json:"errors,omitempty"`
}

// LinearGraphQLError represents an error in a GraphQL response.
type LinearGraphQLError struct {
	Message string `json:"message"`
}

// IssueSearchResponse represents the response from searching issues.
type IssueSearchResponse struct {
	Issues struct {
		Nodes []LinearIssue `json:"nodes"`
	} `json:"issues"`
}

// LinearIssue represents a Linear issue.
type LinearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

// CreateIssueResponse represents the response from creating an issue.
type CreateIssueResponse struct {
	IssueCreate struct {
		Success bool `json:"success"`
		Issue   struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			URL        string `json:"url"`
		} `json:"issue"`
	} `json:"issueCreate"`
}

// CommentCreateResponse represents the response from creating a comment.
type CommentCreateResponse struct {
	CommentCreate struct {
		Success bool `json:"success"`
		Comment struct {
			ID        string    `json:"id"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"comment"`
	} `json:"commentCreate"`
}

// LabelsResponse represents the response from fetching labels.
type LabelsResponse struct {
	IssueLabels struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"issueLabels"`
}

// IssueMetadata contains metadata embedded in issue descriptions.
type IssueMetadata struct {
	TestHash     string `json:"testHash"`
	TestFullPath string `json:"testFullPath"`
	Environment  string `json:"environment"`
	FirstSeen    string `json:"firstSeen"`
}

// Config holds the configuration for the Linear issue creator.
type Config struct {
	APIKey      string
	TeamID      string
	ProjectID   string
	Priority    int
	MaxFailures int
	Environment string
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <test-results.json> <workflow-url> <environment>\n", os.Args[0])
		os.Exit(1)
	}

	testResultsFile := os.Args[1]
	workflowURL := os.Args[2]
	environment := os.Args[3]

	config, err := loadConfig(environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Linear issue creation skipped")
		os.Exit(0)
	}

	if err := processTestFailures(testResultsFile, workflowURL, config); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to create Linear issues: %v\n", err)
		fmt.Fprintln(os.Stderr, "Test results are still available in artifacts")
		os.Exit(0)
	}

	fmt.Println("[LINEAR] Done!")
}

func loadConfig(environment string) (*Config, error) {
	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("LINEAR_API_KEY environment variable not set")
	}

	if !strings.HasPrefix(apiKey, "lin_api_") {
		return nil, fmt.Errorf("LINEAR_API_KEY has invalid format (should start with 'lin_api_')")
	}

	teamID := os.Getenv("LINEAR_TEAM_ID")
	if teamID == "" {
		return nil, fmt.Errorf("LINEAR_TEAM_ID environment variable not set")
	}

	uuidRegex := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	if !uuidRegex.MatchString(teamID) {
		return nil, fmt.Errorf("LINEAR_TEAM_ID is not a valid UUID")
	}

	priority := defaultPriority
	if priorityStr := os.Getenv("LINEAR_PRIORITY"); priorityStr != "" {
		if p, err := strconv.Atoi(priorityStr); err == nil && p >= 1 && p <= 4 {
			priority = p
		}
	}

	maxFailures := defaultMaxFailures
	if maxStr := os.Getenv("MAX_FAILURES"); maxStr != "" {
		if m, err := strconv.Atoi(maxStr); err == nil && m > 0 {
			maxFailures = m
		}
	}

	projectID := os.Getenv("LINEAR_PROJECT_ID")
	if projectID != "" {
		uuidRegex := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
		if !uuidRegex.MatchString(projectID) {
			return nil, fmt.Errorf("LINEAR_PROJECT_ID is not a valid UUID")
		}
	}

	return &Config{
		APIKey:      apiKey,
		TeamID:      teamID,
		ProjectID:   projectID,
		Priority:    priority,
		MaxFailures: maxFailures,
		Environment: environment,
	}, nil
}

func processTestFailures(testResultsFile, workflowURL string, config *Config) error {
	fmt.Println("[LINEAR] Starting Linear issue creation")

	data, err := os.ReadFile(testResultsFile)
	if err != nil {
		return fmt.Errorf("failed to read test results: %w", err)
	}

	var reports []shared.GinkgoReport
	if err := json.Unmarshal(data, &reports); err != nil {
		return fmt.Errorf("failed to parse test results: %w", err)
	}

	if len(reports) == 0 {
		return fmt.Errorf("no test reports found")
	}

	report := reports[0]

	var failures []shared.SpecReport
	passed := 0
	for _, spec := range report.SpecReports {
		if spec.State == "failed" {
			failures = append(failures, spec)
		} else if spec.State == "passed" {
			passed++
		}
	}

	fmt.Printf("[LINEAR] Parsed %d test results: %d failed, %d passed\n",
		len(report.SpecReports), len(failures), passed)

	if len(failures) == 0 {
		fmt.Println("[LINEAR] No test failures - no issues to create")
		return nil
	}

	if len(failures) > config.MaxFailures {
		fmt.Printf("[LINEAR] Found %d failures (max: %d). Skipping Linear issue creation to avoid spam.\n",
			len(failures), config.MaxFailures)
		fmt.Printf("[LINEAR] Please review the workflow run manually: %s\n", workflowURL)
		return nil
	}

	fmt.Printf("[LINEAR] Checking failure threshold: %d <= %d\n", len(failures), config.MaxFailures)
	fmt.Printf("[LINEAR] Processing %d failures...\n", len(failures))

	labelIDs, err := getLabelIDs(config)
	if err != nil {
		fmt.Printf("[LINEAR] Warning: Could not resolve labels: %v\n", err)
		labelIDs = []string{}
	}

	created := 0
	updated := 0
	skipped := 0

	for i, failure := range failures {
		testName := buildTestName(failure)
		fmt.Printf("[LINEAR] [%d/%d] %s\n", i+1, len(failures), testName)

		existingIssue, err := searchExistingIssue(config, failure)
		if err != nil {
			fmt.Printf("[LINEAR]   Warning: Error searching for existing issue: %v\n", err)
		} else if existingIssue != nil {
			fmt.Printf("[LINEAR]   Found existing issue: %s\n", existingIssue.Identifier)
			if err := addComment(config, existingIssue.ID, failure, workflowURL); err != nil {
				fmt.Printf("[LINEAR]   Error adding comment: %v\n", err)
			} else {
				fmt.Printf("[LINEAR]   Updated: %s\n", existingIssue.Identifier)
				updated++
			}
			continue
		}

		fmt.Println("[LINEAR]   No existing issue found")
		fmt.Println("[LINEAR]   Creating new issue...")

		issueURL, err := createIssue(config, failure, workflowURL, labelIDs)
		if err != nil {
			fmt.Printf("[LINEAR]   Error creating issue: %v\n", err)
			skipped++
			continue
		}

		fmt.Printf("[LINEAR]   Created: %s\n", issueURL)
		created++

		if i < len(failures)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	fmt.Printf("[LINEAR] Summary: %d created, %d updated, %d skipped\n", created, updated, skipped)
	return nil
}

func buildTestName(spec shared.SpecReport) string {
	parts := make([]string, 0, len(spec.ContainerHierarchyTexts)+1)
	parts = append(parts, spec.ContainerHierarchyTexts...)
	parts = append(parts, spec.LeafNodeText)
	return strings.Join(parts, " > ")
}

func generateTestHash(spec shared.SpecReport, environment string) string {
	testPath := buildTestName(spec)
	input := fmt.Sprintf("%s|%s", testPath, environment)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("sha256:%x", hash)
}

func buildIssueTitle(spec shared.SpecReport, environment string) string {
	suite := ""
	if len(spec.ContainerHierarchyTexts) > 0 {
		suite = spec.ContainerHierarchyTexts[0]
	}

	testName := spec.LeafNodeText

	if len(testName) > 60 && strings.HasPrefix(strings.ToLower(testName), "should ") {
		testName = testName[7:]
	}

	title := fmt.Sprintf("[Nightly-%s] %s - %s", environment, suite, testName)

	if len(title) > 100 {
		title = title[:97] + "..."
	}

	return title
}

func buildIssueDescription(spec shared.SpecReport, workflowURL, environment string) string {
	var sb strings.Builder

	sb.WriteString("## Test Failure Details\n\n")

	if len(spec.ContainerHierarchyTexts) > 0 {
		sb.WriteString(fmt.Sprintf("**Test Suite**: %s\n", spec.ContainerHierarchyTexts[0]))
	}
	sb.WriteString(fmt.Sprintf("**Environment**: %s\n", environment))
	sb.WriteString(fmt.Sprintf("**First Seen**: %s\n\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC")))

	sb.WriteString("### Test Name\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", buildTestName(spec)))

	if spec.Failure != nil {
		sb.WriteString("### Error Message\n```\n")
		sb.WriteString(spec.Failure.Message)
		sb.WriteString("\n```\n\n")

		fileName := filepath.Base(spec.Failure.Location.FileName)
		sb.WriteString("### Location\n")
		sb.WriteString(fmt.Sprintf("File: `%s`\n", fileName))
		sb.WriteString(fmt.Sprintf("Line: %d\n\n", spec.Failure.Location.LineNumber))
	}

	if spec.CapturedGinkgoWriterOutput != "" {
		sb.WriteString("### Captured Output\n```\n")
		output := spec.CapturedGinkgoWriterOutput
		if len(output) > 1000 {
			output = output[:1000] + "\n... (truncated)"
		}
		sb.WriteString(output)
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("### Workflow Run\n")
	sb.WriteString(fmt.Sprintf("%s\n\n", workflowURL))

	sb.WriteString("---\n\n")

	metadata := IssueMetadata{
		TestHash:     generateTestHash(spec, environment),
		TestFullPath: buildTestName(spec),
		Environment:  environment,
		FirstSeen:    time.Now().UTC().Format(time.RFC3339),
	}

	var metadataBuf bytes.Buffer
	encoder := json.NewEncoder(&metadataBuf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	encoder.Encode(metadata)

	sb.WriteString("<!-- LINEAR_METADATA\n")
	sb.WriteString(strings.TrimSpace(metadataBuf.String()))
	sb.WriteString("\n-->\n")

	return sb.String()
}

func buildCommentBody(spec shared.SpecReport, workflowURL string) string {
	var sb strings.Builder

	sb.WriteString("### New Failure Occurrence\n\n")
	sb.WriteString(fmt.Sprintf("**Date**: %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC")))
	sb.WriteString(fmt.Sprintf("**Workflow Run**: [View run](%s)\n\n", workflowURL))

	if spec.Failure != nil {
		sb.WriteString("**Error Message**:\n```\n")
		sb.WriteString(spec.Failure.Message)
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("The test failed again in the nightly run. This indicates a recurring issue.\n")

	return sb.String()
}

func searchExistingIssue(config *Config, spec shared.SpecReport) (*LinearIssue, error) {
	fmt.Println("[LINEAR]   Searching for existing issue...")

	testHash := generateTestHash(spec, config.Environment)

	query := `
		query SearchIssues($teamId: ID!) {
			issues(
				filter: {
					team: { id: { eq: $teamId } }
					state: { type: { nin: ["completed", "canceled"] } }
				}
				first: 50
			) {
				nodes {
					id
					identifier
					title
					description
					state { name type }
					labels { nodes { id name } }
				}
			}
		}
	`

	variables := map[string]interface{}{
		"teamId": config.TeamID,
	}

	resp, err := executeGraphQLQuery(config.APIKey, query, variables)
	if err != nil {
		return nil, err
	}

	var searchResp IssueSearchResponse
	if err := json.Unmarshal(resp, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search response: %w", err)
	}

	for _, issue := range searchResp.Issues.Nodes {
		if metadata := extractMetadata(issue.Description); metadata != nil {
			if metadata.TestHash == testHash && metadata.Environment == config.Environment {
				return &issue, nil
			}
		}
	}

	return nil, nil
}

func extractMetadata(description string) *IssueMetadata {
	re := regexp.MustCompile(`(?s)<!-- LINEAR_METADATA\s*\n(.*?)\n\\?-->`)
	matches := re.FindStringSubmatch(description)
	if len(matches) < 2 {
		return nil
	}

	var metadata IssueMetadata
	if err := json.Unmarshal([]byte(matches[1]), &metadata); err != nil {
		return nil
	}

	return &metadata
}

func createIssue(config *Config, spec shared.SpecReport, workflowURL string, labelIDs []string) (string, error) {
	title := buildIssueTitle(spec, config.Environment)
	description := buildIssueDescription(spec, workflowURL, config.Environment)

	mutation := `
		mutation CreateIssue($teamId: String!, $title: String!, $description: String!, $priority: Int, $labelIds: [String!], $projectId: String) {
			issueCreate(input: {
				teamId: $teamId
				title: $title
				description: $description
				priority: $priority
				labelIds: $labelIds
				projectId: $projectId
			}) {
				success
				issue {
					id
					identifier
					url
				}
			}
		}
	`

	variables := map[string]interface{}{
		"teamId":      config.TeamID,
		"title":       title,
		"description": description,
		"priority":    config.Priority,
		"labelIds":    labelIDs,
	}

	if config.ProjectID != "" {
		variables["projectId"] = config.ProjectID
	} else {
		variables["projectId"] = nil
	}

	resp, err := executeGraphQLQuery(config.APIKey, mutation, variables)
	if err != nil {
		return "", err
	}

	var createResp CreateIssueResponse
	if err := json.Unmarshal(resp, &createResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal create response: %w", err)
	}

	if !createResp.IssueCreate.Success {
		return "", fmt.Errorf("issue creation returned success=false")
	}

	return createResp.IssueCreate.Issue.URL, nil
}

func addComment(config *Config, issueID string, spec shared.SpecReport, workflowURL string) error {
	fmt.Println("[LINEAR]   Adding comment with new failure...")

	body := buildCommentBody(spec, workflowURL)

	mutation := `
		mutation AddComment($issueId: String!, $body: String!) {
			commentCreate(input: {
				issueId: $issueId
				body: $body
			}) {
				success
				comment {
					id
					createdAt
				}
			}
		}
	`

	variables := map[string]interface{}{
		"issueId": issueID,
		"body":    body,
	}

	resp, err := executeGraphQLQuery(config.APIKey, mutation, variables)
	if err != nil {
		return err
	}

	var commentResp CommentCreateResponse
	if err := json.Unmarshal(resp, &commentResp); err != nil {
		return fmt.Errorf("failed to unmarshal comment response: %w", err)
	}

	if !commentResp.CommentCreate.Success {
		return fmt.Errorf("comment creation returned success=false")
	}

	return nil
}

func getLabelIDs(config *Config) ([]string, error) {
	envLabel := strings.ToUpper(string(config.Environment[0])) + strings.ToLower(config.Environment[1:])

	requiredLabels := []string{
		"Automation Failures",
		"nightly-failure",
		envLabel,
	}

	query := `
		query GetLabels {
			issueLabels {
				nodes {
					id
					name
				}
			}
		}
	`

	variables := map[string]interface{}{}

	resp, err := executeGraphQLQuery(config.APIKey, query, variables)
	if err != nil {
		return nil, err
	}

	var labelsResp LabelsResponse
	if err := json.Unmarshal(resp, &labelsResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels response: %w", err)
	}

	labelMap := make(map[string]string)
	for _, label := range labelsResp.IssueLabels.Nodes {
		labelMap[label.Name] = label.ID
	}

	var labelIDs []string
	for _, labelName := range requiredLabels {
		if id, found := labelMap[labelName]; found {
			labelIDs = append(labelIDs, id)
		} else {
			fmt.Printf("[LINEAR] Warning: Label '%s' not found in Linear workspace\n", labelName)
		}
	}

	return labelIDs, nil
}

func executeGraphQLQuery(apiKey, query string, variables map[string]interface{}) (json.RawMessage, error) {
	request := LinearGraphQLRequest{
		Query:     query,
		Variables: variables,
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			waitTime := time.Duration(1<<uint(attempt-1)) * time.Second
			fmt.Printf("[LINEAR] Retrying after %v (attempt %d/%d)...\n", waitTime, attempt+1, maxRetries)
			time.Sleep(waitTime)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearAPIEndpoint, bytes.NewReader(payload))

		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", apiKey)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to execute request: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds <= 60 {
					fmt.Printf("[LINEAR] Rate limited. Waiting %d seconds...\n", seconds)
					time.Sleep(time.Duration(seconds) * time.Second)
					continue
				}
			}
			lastErr = fmt.Errorf("rate limited (429)")
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("linear API returned status %d: %s", resp.StatusCode, string(body))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}

		var graphQLResp LinearGraphQLResponse
		if err := json.Unmarshal(body, &graphQLResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		if len(graphQLResp.Errors) > 0 {
			errMsgs := make([]string, len(graphQLResp.Errors))
			for i, e := range graphQLResp.Errors {
				errMsgs[i] = e.Message
			}
			return nil, fmt.Errorf("linear API errors: %s", strings.Join(errMsgs, "; "))
		}

		return graphQLResp.Data, nil
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}
