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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Ginkgo input types ---

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

// --- JUnit XML input types ---

// JUnitTestSuites represents the root element of a JUnit XML report.
type JUnitTestSuites struct {
	XMLName    xml.Name         `xml:"testsuites"`
	TestSuites []JUnitTestSuite `xml:"testsuite"`
}

// JUnitTestSuite represents a single test suite in a JUnit XML report.
type JUnitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      float64         `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr"`
	TestCases []JUnitTestCase `xml:"testcase"`
}

// JUnitTestCase represents a single test case in a JUnit XML report.
type JUnitTestCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *JUnitFailure `xml:"failure"`
	Error     *JUnitFailure `xml:"error"`
}

// JUnitFailure holds the message and text of a test failure or error.
type JUnitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// --- Common intermediate representation ---

type testStats struct {
	passed  int
	failed  int
	skipped int
	total   int
}

type failureDetail struct {
	testName string
	location string
	errorMsg string
	output   string
}

type reportData struct {
	suiteName string
	succeeded bool
	startTime time.Time
	duration  time.Duration
	stats     testStats
	failures  []failureDetail
}

// --- Parsers ---

func parseGinkgoReport(data []byte) (reportData, error) {
	var reports []GinkgoReport
	if err := json.Unmarshal(data, &reports); err != nil {
		return reportData{}, fmt.Errorf("parsing ginkgo JSON: %w", err)
	}

	if len(reports) == 0 {
		return reportData{}, fmt.Errorf("no test reports found")
	}

	r := reports[0]

	var stats testStats
	var failures []failureDetail

	for _, spec := range r.SpecReports {
		switch spec.State {
		case "passed":
			stats.passed++
		case "failed":
			stats.failed++
			failures = append(failures, ginkgoFailureDetail(spec))
		case "skipped":
			stats.skipped++
		}
	}

	stats.total = stats.passed + stats.failed + stats.skipped

	return reportData{
		suiteName: r.SuiteDescription,
		succeeded: r.SuiteSucceeded,
		startTime: r.StartTime,
		duration:  time.Duration(r.RunTime),
		stats:     stats,
		failures:  failures,
	}, nil
}

func ginkgoFailureDetail(spec SpecReport) failureDetail {
	parts := make([]string, 0, len(spec.ContainerHierarchyTexts)+1)
	parts = append(parts, spec.ContainerHierarchyTexts...)
	parts = append(parts, spec.LeafNodeText)

	d := failureDetail{testName: strings.Join(parts, " > ")}

	if spec.Failure != nil {
		d.location = fmt.Sprintf("%s:%d", filepath.Base(spec.Failure.Location.FileName), spec.Failure.Location.LineNumber)
		d.errorMsg = spec.Failure.Message
		if len(d.errorMsg) > 500 {
			d.errorMsg = d.errorMsg[:500] + "..."
		}
	}

	if spec.CapturedGinkgoWriterOutput != "" {
		d.output = spec.CapturedGinkgoWriterOutput
		if len(d.output) > 300 {
			d.output = d.output[:300] + "..."
		}
	}

	return d
}

func parseJUnitReport(data []byte) (reportData, error) {
	var suites JUnitTestSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		return reportData{}, fmt.Errorf("parsing JUnit XML: %w", err)
	}

	if len(suites.TestSuites) == 0 {
		return reportData{}, fmt.Errorf("no test suites found in JUnit XML")
	}

	var (
		stats     testStats
		failures  []failureDetail
		totalSecs float64
		startTime time.Time
	)

	for _, s := range suites.TestSuites {
		failed := s.Failures + s.Errors
		stats.failed += failed
		stats.skipped += s.Skipped
		stats.passed += s.Tests - failed - s.Skipped
		stats.total += s.Tests
		totalSecs += s.Time

		if t, err := time.Parse("2006-01-02T15:04:05", s.Timestamp); err == nil {
			if startTime.IsZero() || t.Before(startTime) {
				startTime = t
			}
		}

		for _, tc := range s.TestCases {
			f := tc.Failure
			if f == nil {
				f = tc.Error
			}

			if f == nil {
				continue
			}

			errorMsg := strings.TrimSpace(f.Text)
			if errorMsg == "" {
				errorMsg = f.Message
			}

			if len(errorMsg) > 500 {
				errorMsg = errorMsg[:500] + "..."
			}

			failures = append(failures, failureDetail{
				testName: tc.ClassName + " > " + tc.Name,
				errorMsg: errorMsg,
			})
		}
	}

	if startTime.IsZero() {
		startTime = time.Now().UTC()
	}

	return reportData{
		suiteName: getTitle(),
		succeeded: stats.failed == 0,
		startTime: startTime,
		duration:  time.Duration(totalSecs * float64(time.Second)),
		stats:     stats,
		failures:  failures,
	}, nil
}

// --- Slack types ---

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

// --- Message builder ---

func buildSlackMessage(report reportData, workflowURL string) SlackMessage {
	statusEmoji := ":white_check_mark:"
	statusLabel := "PASSED"

	if !report.succeeded {
		statusEmoji = ":x:"
		statusLabel = "FAILED"
	}

	environment := getEnvironment()
	headerTitle := fmt.Sprintf("%s (%s)", getTitle(), strings.ToUpper(environment))
	headerText := fmt.Sprintf("%s *%s* - %s", statusEmoji, report.suiteName, statusLabel)

	blocks := []SlackBlock{
		{
			Type: "header",
			Text: &SlackText{Type: "plain_text", Text: headerTitle},
		},
		{
			Type: "section",
			Text: &SlackText{Type: "mrkdwn", Text: headerText},
		},
		{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*Total Tests:*\n%d", report.stats.total)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Duration:*\n%s", formatDuration(report.duration))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Passed:*\n%d", report.stats.passed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Failed:*\n%d", report.stats.failed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Skipped:*\n%d", report.stats.skipped)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Time:*\n%s", report.startTime.Format("2006-01-02 15:04:05"))},
			},
		},
	}

	if len(report.failures) > 0 {
		blocks = append(blocks, SlackBlock{Type: "divider"})
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{Type: "mrkdwn", Text: "*Failed Tests:*"},
		})

		for i, f := range report.failures {
			if i >= 5 {
				blocks = append(blocks, SlackBlock{
					Type: "section",
					Text: &SlackText{
						Type: "mrkdwn",
						Text: fmt.Sprintf("_...and %d more failures_", len(report.failures)-5),
					},
				})

				break
			}

			blocks = append(blocks, SlackBlock{
				Type: "section",
				Text: &SlackText{Type: "mrkdwn", Text: formatFailure(f)},
			})
		}
	}

	blocks = append(blocks, SlackBlock{Type: "divider"})
	blocks = append(blocks, SlackBlock{
		Type: "section",
		Text: &SlackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("<%s|View Full Report on GitHub Actions>", workflowURL),
		},
	})

	return SlackMessage{Blocks: blocks}
}

func formatFailure(f failureDetail) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("*Test:* %s\n", f.testName))

	if f.location != "" {
		sb.WriteString(fmt.Sprintf("*Location:* `%s`\n", f.location))
	}

	if f.errorMsg != "" {
		sb.WriteString(fmt.Sprintf("*Error:*\n```\n%s\n```", f.errorMsg))
	}

	if f.output != "" {
		sb.WriteString(fmt.Sprintf("\n*Output:*\n```\n%s\n```", f.output))
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

// --- Helpers ---

func getEnvironment() string {
	if v := os.Getenv("ENVIRONMENT"); v != "" {
		return v
	}

	return "unknown"
}

func getTitle() string {
	if v := os.Getenv("TITLE"); v != "" {
		return v
	}

	return "API Test Results"
}

// --- HTTP ---

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

	resp, err := (&http.Client{}).Do(req)
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

// --- Entry point ---

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

	format := os.Getenv("FORMAT")
	if format == "" {
		format = "ginkgo"
	}

	var report reportData

	switch format {
	case "ginkgo":
		report, err = parseGinkgoReport(data)
	case "junit":
		report, err = parseJUnitReport(data)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown FORMAT %q (must be ginkgo or junit)\n", format)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing test results: %v\n", err)
		os.Exit(1)
	}

	message := buildSlackMessage(report, workflowURL)
	if err := sendSlackMessage(webhookURL, message); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending Slack message: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Slack notification sent successfully")
}
