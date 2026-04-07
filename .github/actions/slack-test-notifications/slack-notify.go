/*
Copyright 2025 the Unikorn Authors.
Copyright 2026 Nscale.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GinkgoReport represents the JSON output from Ginkgo test runs.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type GinkgoReport struct {
	SuitePath        string       `json:"SuitePath"`
	SuiteDescription string       `json:"SuiteDescription"`
	SuiteSucceeded   bool         `json:"SuiteSucceeded"`
	PreRunStats      PreRunStats  `json:"PreRunStats"`
	StartTime        time.Time    `json:"StartTime"`
	EndTime          time.Time    `json:"EndTime"`
	RunTime          int64        `json:"RunTime"`
	SpecReports      []SpecReport `json:"SpecReports"`
}

// PreRunStats contains test statistics before execution.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type PreRunStats struct {
	TotalSpecs       int `json:"TotalSpecs"`
	SpecsThatWillRun int `json:"SpecsThatWillRun"`
}

// SpecReport contains the results of a single test spec.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type SpecReport struct {
	ContainerHierarchyTexts    []string     `json:"ContainerHierarchyTexts"`
	LeafNodeText               string       `json:"LeafNodeText"`
	State                      string       `json:"State"` // passed, failed, skipped, etc.
	RunTime                    int64        `json:"RunTime"`
	Failure                    *SpecFailure `json:"Failure,omitempty"`
	CapturedGinkgoWriterOutput string       `json:"CapturedGinkgoWriterOutput"`
}

// SpecFailure contains failure details for a test spec.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type SpecFailure struct {
	Message  string   `json:"Message"`
	Location Location `json:"Location"`
}

// Location represents a file location.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type Location struct {
	FileName   string `json:"FileName"`
	LineNumber int    `json:"LineNumber"`
}

// SlackMessage represents the Slack webhook payload.
type SlackMessage struct {
	Text        string            `json:"text,omitempty"`
	Blocks      []SlackBlock      `json:"blocks,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

type SlackBlock struct {
	Type      string      `json:"type"`
	Text      *SlackText  `json:"text,omitempty"`
	Fields    []SlackText `json:"fields,omitempty"`
	Accessory interface{} `json:"accessory,omitempty"`
}

type SlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type SlackAttachment struct {
	Color  string       `json:"color"`
	Blocks []SlackBlock `json:"blocks"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <test-results.json> <workflow-url>\n", os.Args[0])
		os.Exit(1)
	}

	testResultsFile := os.Args[1]
	workflowURL := os.Args[2]

	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		fmt.Fprintln(os.Stderr, "Error: SLACK_WEBHOOK_URL environment variable not set")
		os.Exit(1)
	}

	data, err := os.ReadFile(testResultsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading test results: %v\n", err)
		os.Exit(1)
	}

	var reports []GinkgoReport
	if err := json.Unmarshal(data, &reports); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing test results: %v\n", err)
		os.Exit(1)
	}

	if len(reports) == 0 {
		fmt.Fprintln(os.Stderr, "No test reports found")
		os.Exit(1)
	}

	message := buildSlackMessage(reports[0], workflowURL)
	if err := sendSlackMessage(webhookURL, message); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending Slack message: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Slack notification sent successfully")
}

type testStats struct {
	passed   int
	failed   int
	skipped  int
	total    int
	failures []SpecReport
}

func calculateTestStats(specs []SpecReport) testStats {
	var stats testStats

	for _, spec := range specs {
		switch spec.State {
		case "passed":
			stats.passed++
		case "failed":
			stats.failed++
			stats.failures = append(stats.failures, spec)
		case "skipped":
			stats.skipped++
		}
	}

	stats.total = stats.passed + stats.failed + stats.skipped

	return stats
}

func getEnvironment() string {
	environment := os.Getenv("ENVIRONMENT")
	if environment == "" {
		environment = "unknown"
	}

	return environment
}

func getTitle() string {
	title := os.Getenv("TITLE")
	if title == "" {
		title = "API Test Results"
	}

	return title
}

func buildSlackMessage(report GinkgoReport, workflowURL string) SlackMessage {
	stats := calculateTestStats(report.SpecReports)
	duration := time.Duration(report.RunTime)

	statusEmoji := ":white_check_mark:"
	if !report.SuiteSucceeded {
		statusEmoji = ":x:"
	}

	environment := getEnvironment()

	headerText := fmt.Sprintf("%s *%s*", statusEmoji, report.SuiteDescription)
	if !report.SuiteSucceeded {
		headerText += " - FAILED"
	} else {
		headerText += " - PASSED"
	}

	headerTitle := fmt.Sprintf("%s (%s)", getTitle(), strings.ToUpper(environment))

	blocks := []SlackBlock{
		{
			Type: "header",
			Text: &SlackText{
				Type: "plain_text",
				Text: headerTitle,
			},
		},
		{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: headerText,
			},
		},
		{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*Total Tests:*\n%d", stats.total)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Duration:*\n%s", formatDuration(duration))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Passed:*\n%d", stats.passed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Failed:*\n%d", stats.failed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Skipped:*\n%d", stats.skipped)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Time:*\n%s", report.StartTime.Format("2006-01-02 15:04:05"))},
			},
		},
	}

	if len(stats.failures) > 0 {
		blocks = append(blocks, SlackBlock{
			Type: "divider",
		})

		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: "*Failed Tests:*",
			},
		})

		for i, failure := range stats.failures {
			if i >= 5 {
				remaining := len(stats.failures) - 5
				blocks = append(blocks, SlackBlock{
					Type: "section",
					Text: &SlackText{
						Type: "mrkdwn",
						Text: fmt.Sprintf("_...and %d more failures_", remaining),
					},
				})

				break
			}

			blocks = append(blocks, SlackBlock{
				Type: "section",
				Text: &SlackText{
					Type: "mrkdwn",
					Text: formatFailure(failure),
				},
			})
		}
	}

	blocks = append(blocks, SlackBlock{
		Type: "divider",
	})

	blocks = append(blocks, SlackBlock{
		Type: "section",
		Text: &SlackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("<%s|View Full Report on GitHub Actions>", workflowURL),
		},
	})

	return SlackMessage{
		Blocks: blocks,
	}
}

func buildTestName(spec SpecReport) string {
	parts := make([]string, 0, len(spec.ContainerHierarchyTexts)+1)
	parts = append(parts, spec.ContainerHierarchyTexts...)
	parts = append(parts, spec.LeafNodeText)

	return strings.Join(parts, " > ")
}

func formatFailure(spec SpecReport) string {
	testName := buildTestName(spec)

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("*Test:* %s\n", testName))

	if spec.Failure != nil {
		fileName := filepath.Base(spec.Failure.Location.FileName)
		sb.WriteString(fmt.Sprintf("*Location:* `%s:%d`\n", fileName, spec.Failure.Location.LineNumber))

		errorMsg := spec.Failure.Message
		if len(errorMsg) > 500 {
			errorMsg = errorMsg[:500] + "..."
		}

		sb.WriteString(fmt.Sprintf("*Error:*\n```\n%s\n```", errorMsg))
	}

	if spec.CapturedGinkgoWriterOutput != "" {
		output := spec.CapturedGinkgoWriterOutput
		if len(output) > 300 {
			output = output[:300] + "..."
		}

		sb.WriteString(fmt.Sprintf("\n*Output:*\n```\n%s\n```", output))
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	return fmt.Sprintf("%.1fm", d.Minutes())
}

func sendSlackMessage(webhookURL string, message SlackMessage) error {
	payload, err := json.MarshalIndent(message, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))

	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post message: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		//nolint:err113 // Dynamic error needed to include HTTP status and response body
		return fmt.Errorf("slack API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
