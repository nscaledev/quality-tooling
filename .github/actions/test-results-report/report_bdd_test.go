package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTestResultsReportBDD(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test Results Report BDD Suite")
}

var _ = Describe("Test Results Report", func() {
	Context("When comparing Playwright results", func() {
		Describe("Given repeated Playwright test titles under different describe blocks", func() {
			var (
				currentInput  []byte
				previousInput []byte
			)

			BeforeEach(func() {
				currentInput = []byte(`{
  "suites": [{
    "title": "src/spec/resources.spec.ts",
    "file": "src/spec/resources.spec.ts",
    "suites": [{
      "title": "create dialog",
      "specs": [{
        "title": "submits form",
        "tests": [{
          "projectName": "chromium",
          "status": "unexpected",
          "results": [{"status": "failed", "duration": 1000, "error": {"message": "create failed"}}]
        }]
      }]
    }, {
      "title": "delete dialog",
      "specs": [{
        "title": "submits form",
        "tests": [{
          "projectName": "chromium",
          "status": "unexpected",
          "results": [{"status": "failed", "duration": 1000, "error": {"message": "delete failed"}}]
        }]
      }]
    }]
  }]
}`)
				previousInput = []byte(`{
  "suites": [{
    "title": "src/spec/resources.spec.ts",
    "file": "src/spec/resources.spec.ts",
    "suites": [{
      "title": "create dialog",
      "specs": [{
        "title": "submits form",
        "tests": [{
          "projectName": "chromium",
          "status": "unexpected",
          "results": [{"status": "failed", "duration": 1000, "error": {"message": "create failed before"}}]
        }]
      }]
    }, {
      "title": "delete dialog",
      "specs": [{
        "title": "submits form",
        "tests": [{
          "projectName": "chromium",
          "status": "expected",
          "results": [{"status": "passed", "duration": 1000}]
        }]
      }]
    }]
  }]
}`)
			})

			It("should produce distinct stable identities for each describe path", func() {
				current, err := parsePlaywrightJSON(currentInput)
				Expect(err).NotTo(HaveOccurred())
				Expect(current.Tests).To(HaveLen(2))
				Expect(current.Tests[0].ID).NotTo(Equal(current.Tests[1].ID))

				Expect(current.Tests[0].ID).To(And(
					ContainSubstring("src/spec/resources.spec.ts"),
					ContainSubstring("create dialog"),
					ContainSubstring("submits form"),
					ContainSubstring("chromium"),
				))
				Expect(current.Tests[1].ID).To(And(
					ContainSubstring("src/spec/resources.spec.ts"),
					ContainSubstring("delete dialog"),
					ContainSubstring("submits form"),
					ContainSubstring("chromium"),
				))

				GinkgoWriter.Printf("Verified distinct Playwright IDs: %s / %s\n", current.Tests[0].ID, current.Tests[1].ID)
			})

			It("should keep recurring and newly failing tests separate", func() {
				current, err := parsePlaywrightJSON(currentInput)
				Expect(err).NotTo(HaveOccurred())

				previous, err := parsePlaywrightJSON(previousInput)
				Expect(err).NotTo(HaveOccurred())

				analysis := analyze(current, &previous)
				Expect(analysis.Compare).NotTo(BeNil())
				Expect(analysis.Compare.RecurringFailures).To(HaveLen(1))
				Expect(analysis.Compare.NewFailures).To(HaveLen(1))
				Expect(analysis.Compare.RecurringFailures[0].ID).To(And(
					ContainSubstring("create dialog"),
					ContainSubstring("submits form"),
				))
				Expect(analysis.Compare.NewFailures[0].ID).To(And(
					ContainSubstring("delete dialog"),
					ContainSubstring("submits form"),
				))

				GinkgoWriter.Printf("Recurring failure: %s\n", analysis.Compare.RecurringFailures[0].ID)
				GinkgoWriter.Printf("New failure: %s\n", analysis.Compare.NewFailures[0].ID)
			})
		})
	})

	Context("When running the report action", func() {
		Describe("Given current and previous JUnit results", func() {
			var (
				dir          string
				currentPath  string
				previousPath string
				summaryPath  string
				outputPath   string
				config       Config
			)

			BeforeEach(func() {
				dir = GinkgoT().TempDir()
				currentPath = filepath.Join(dir, "current.xml")
				previousPath = filepath.Join(dir, "previous.xml")
				summaryPath = filepath.Join(dir, "summary.md")
				outputPath = filepath.Join(dir, "outputs.txt")

				writeTestFile(currentPath, `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="Console E2E" tests="3" failures="1" skipped="1" time="12.5">
  <testsuite name="chromium" tests="3" failures="1" skipped="1" time="12.5">
    <testcase classname="settings.organisation" name="creates organisation group" time="1.2"/>
    <testcase classname="compute.instance" name="creates instance" time="8.5">
      <failure message="Expected button to be visible">TimeoutError at src/spec/compute/instance.spec.ts:42:11</failure>
    </testcase>
    <testcase classname="network.vpc" name="deletes VPC" time="0">
      <skipped message="feature flag disabled"/>
    </testcase>
  </testsuite>
</testsuites>`)
				writeTestFile(previousPath, `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="Console E2E" tests="2" failures="1" skipped="0" time="10">
  <testsuite name="chromium" tests="2" failures="1" skipped="0" time="10">
    <testcase classname="settings.organisation" name="creates organisation group" time="1.1">
      <failure message="Previous intermittent failure"/>
    </testcase>
    <testcase classname="compute.instance" name="creates instance" time="7">
      <failure message="Expected button to be visible"/>
    </testcase>
  </testsuite>
</testsuites>`)

				config = Config{
					TestResultsPath:       currentPath,
					Format:                formatJUnit,
					PreviousResultsPath:   previousPath,
					PreviousResultsFormat: formatJUnit,
					PreviousResultsSource: "path",
					CompareWithPrevious:   true,
					WriteStepSummary:      true,
					StepSummaryPath:       summaryPath,
					Title:                 "E2E Test Results",
					Environment:           "dev",
					MaxFailures:           5,
					MaxSkips:              10,
					IncludeSkips:          true,
				}

				setEnv("GITHUB_OUTPUT", outputPath)
			})

			It("should write the step summary and GitHub outputs", func() {
				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())

				summary := readTestFile(summaryPath)
				Expect(summary).To(ContainSubstring("## E2E Test Results"))
				Expect(summary).To(ContainSubstring("**Environment:** `dev`"))
				Expect(summary).To(ContainSubstring("### Previous Result Comparison"))
				Expect(summary).To(ContainSubstring("### Failed Tests"))
				Expect(summary).To(ContainSubstring("### Skipped Tests"))

				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("total", "3"))
				Expect(outputs).To(HaveKeyWithValue("passed", "1"))
				Expect(outputs).To(HaveKeyWithValue("failed", "1"))
				Expect(outputs).To(HaveKeyWithValue("skipped", "1"))
				Expect(outputs).To(HaveKeyWithValue("duration", "12.5s"))
				Expect(outputs).To(HaveKeyWithValue("duration-ms", "12500"))
				Expect(outputs).To(HaveKeyWithValue("conclusion", "failure"))
				Expect(outputs).To(HaveKeyWithValue("new-failures", "0"))
				Expect(outputs).To(HaveKeyWithValue("recurring-failures", "1"))
				Expect(outputs).To(HaveKeyWithValue("resolved-failures", "1"))
				Expect(outputs).To(HaveKeyWithValue("new-skips", "1"))
				Expect(outputs).To(HaveKeyWithValue("slack-sent", "false"))

				outputLines := strings.Split(strings.TrimSpace(readTestFile(outputPath)), "\n")
				Expect(outputLines[:8]).To(Equal([]string{
					"total=3",
					"passed=1",
					"failed=1",
					"skipped=1",
					"duration=12.5s",
					"duration-ms=12500",
					"conclusion=failure",
					"new-failures=0",
				}))

				GinkgoWriter.Printf("Report outputs: %+v\n", outputs)
			})

			It("should wire successful AI analysis into the summary and Slack without raw test detail tables", func() {
				var slackPayload SlackPayload
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					Expect(json.NewDecoder(request.Body).Decode(&slackPayload)).To(Succeed())
					w.WriteHeader(http.StatusOK)
				}))
				defer server.Close()

				previousRunner := runAIAnalysis
				runAIAnalysis = func(_ context.Context, receivedConfig Config, analysis Analysis) (*AIAnalysis, error) {
					Expect(receivedConfig.EnableAIAnalysis).To(BeTrue())
					Expect(receivedConfig.MaxFailures).To(Equal(5))
					Expect(analysis.Failures).To(HaveLen(1))
					Expect(analysis.Skipped).To(HaveLen(1))
					return &AIAnalysis{
						StepSummary:  "## Test Failure Analysis\n\nAI grouped failure summary.",
						SlackSummary: "- *Compute* (infra/external): AI grouped Slack summary.",
					}, nil
				}
				DeferCleanup(func() {
					runAIAnalysis = previousRunner
				})

				config.EnableAIAnalysis = true
				config.ClaudeToken = "test-claude-token"
				config.SendSlack = true
				config.SlackWebhookURL = server.URL

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())

				summary := readTestFile(summaryPath)
				Expect(summary).To(ContainSubstring("## E2E Test Results"))
				Expect(summary).To(ContainSubstring("## Test Failure Analysis"))
				Expect(summary).To(ContainSubstring("AI grouped failure summary."))
				Expect(summary).NotTo(ContainSubstring("### Failed Tests"))
				Expect(summary).NotTo(ContainSubstring("### Skipped Tests"))
				Expect(summary).NotTo(ContainSubstring("Expected button to be visible"))

				slackText := slackPayloadText(slackPayload)
				Expect(slackText).To(ContainSubstring(":mag: *Failure Analysis*"))
				Expect(slackText).To(ContainSubstring("- *Compute* (infra/external): AI grouped Slack summary."))
				Expect(slackText).NotTo(ContainSubstring("*Failed Tests:*"))
				Expect(slackText).NotTo(ContainSubstring("*Test:* creates instance"))

				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("slack-sent", "true"))
			})

			It("should run AI analysis and Slack before test history OTLP shipping", func() {
				var aiCalled atomic.Bool
				var slackCalled atomic.Bool
				var otlpSawAI atomic.Bool
				var otlpSawSlack atomic.Bool
				var slackPayload SlackPayload

				slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					Expect(json.NewDecoder(request.Body).Decode(&slackPayload)).To(Succeed())
					slackCalled.Store(true)
					w.WriteHeader(http.StatusOK)
				}))
				defer slackServer.Close()

				otlpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					otlpSawAI.Store(aiCalled.Load())
					otlpSawSlack.Store(slackCalled.Load())
					http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
				}))
				defer otlpServer.Close()

				previousRunner := runAIAnalysis
				runAIAnalysis = func(_ context.Context, receivedConfig Config, analysis Analysis) (*AIAnalysis, error) {
					Expect(receivedConfig.EnableAIAnalysis).To(BeTrue())
					Expect(analysis.Failures).To(HaveLen(1))
					aiCalled.Store(true)
					return &AIAnalysis{
						StepSummary:  "## Test Failure Analysis\n\nAI analysis completed before OTLP shipping.",
						SlackSummary: "- *Compute* (infra/external): AI Slack summary was ready before OTLP shipping.",
					}, nil
				}
				DeferCleanup(func() {
					runAIAnalysis = previousRunner
				})

				config.EnableAIAnalysis = true
				config.ClaudeToken = "test-claude-token"
				config.SendSlack = true
				config.SlackWebhookURL = slackServer.URL
				config.PublishTestHistory = true
				config.TestHistoryPublishMode = "otlp"
				config.TestHistoryOTLPEndpoint = otlpServer.URL + "/v1/logs"
				config.TestHistoryOutputPath = filepath.Join(dir, ".test-history", "events.ndjson")
				config.TestHistoryTimeout = time.Second
				config.TestHistoryRetries = 0

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())
				Expect(aiCalled.Load()).To(BeTrue())
				Expect(slackCalled.Load()).To(BeTrue())
				Expect(otlpSawAI.Load()).To(BeTrue())
				Expect(otlpSawSlack.Load()).To(BeTrue())
				Expect(slackPayloadText(slackPayload)).To(ContainSubstring("AI Slack summary was ready before OTLP shipping."))

				summary := readTestFile(summaryPath)
				Expect(summary).To(ContainSubstring("AI analysis completed before OTLP shipping."))

				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("slack-sent", "true"))
				Expect(outputs).To(HaveKeyWithValue("test-history-shipping-status", "failed"))
				Expect(outputs).To(HaveKeyWithValue("test-history-posted", "false"))
			})

			It("should plan backend Grafana queries, fetch MCP logs, and pass them into the final AI report", func() {
				writeTestFile(currentPath, `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="Console E2E" tests="2" failures="2" skipped="0" time="18">
  <testsuite name="chromium" tests="2" failures="2" skipped="0" time="18">
    <testcase classname="storage.file" name="uploads file" time="11">
      <failure message="POST /api/storage returned 500 for claim-123">Timeout waiting for file storage upload to finish</failure>
    </testcase>
    <testcase classname="visual.button" name="button color" time="7">
      <failure message="expected CSS color to match">client-side visual assertion failed</failure>
    </testcase>
  </testsuite>
</testsuites>`)

				var queryCount atomic.Int32
				var executedLogQL string
				mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					Expect(request.URL.Path).To(Equal("/mcp"))

					var rpc struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					Expect(json.NewDecoder(request.Body).Decode(&rpc)).To(Succeed())

					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Mcp-Session-Id", "test-session")

					writeRPCResponse := func(result any) {
						Expect(json.NewEncoder(w).Encode(map[string]any{
							"jsonrpc": "2.0",
							"id":      json.RawMessage(rpc.ID),
							"result":  result,
						})).To(Succeed())
					}
					writeToolResponse := func(text string) {
						writeRPCResponse(map[string]any{
							"content": []map[string]string{{
								"type": "text",
								"text": text,
							}},
						})
					}

					switch rpc.Method {
					case "initialize":
						writeRPCResponse(map[string]any{
							"protocolVersion": mcpProtocolVersion,
							"capabilities":    map[string]any{},
							"serverInfo":      map[string]string{"name": "fake-grafana-mcp"},
						})
					case "notifications/initialized":
						w.WriteHeader(http.StatusAccepted)
					case "tools/call":
						var params struct {
							Name      string          `json:"name"`
							Arguments json.RawMessage `json:"arguments"`
						}
						Expect(json.Unmarshal(rpc.Params, &params)).To(Succeed())

						switch params.Name {
						case "list_datasources":
							writeToolResponse(`{"datasources":[{"uid":"loki-dev","name":"Loki","type":"loki","isDefault":true}]}`)
						case "query_loki_logs":
							queryCount.Add(1)
							var args map[string]any
							Expect(json.Unmarshal(params.Arguments, &args)).To(Succeed())
							Expect(args).To(HaveKeyWithValue("datasourceUid", "loki-dev"))
							Expect(args).To(HaveKeyWithValue("startRfc3339", "2026-06-01T13:00:00Z"))
							Expect(args).To(HaveKeyWithValue("endRfc3339", "2026-06-01T14:00:00Z"))
							executedLogQL = args["logql"].(string)
							Expect(executedLogQL).To(ContainSubstring("claim-123"))
							Expect(executedLogQL).To(ContainSubstring("file-storage"))
							Expect(executedLogQL).NotTo(ContainSubstring("visual.button"))
							writeToolResponse(`{"data":[{"timestamp":"1780322400000000000","line":"file-storage controller failed claim-123 with backend 500","labels":{"namespace":"file-storage","pod":"file-storage-api-123"}}],"metadata":{"linesReturned":1,"resultsTruncated":false}}`)
						default:
							Fail("unexpected MCP tool call: " + params.Name)
						}
					default:
						Fail("unexpected MCP method: " + rpc.Method)
					}
				}))
				defer mcpServer.Close()

				previousPlanner := runGrafanaLogQueryPlanning
				previousRunner := runAIAnalysis
				runGrafanaLogQueryPlanning = func(_ context.Context, receivedConfig Config, analysis Analysis) ([]GrafanaLogPlannedQuery, error) {
					Expect(receivedConfig.EnableAIAnalysis).To(BeTrue())
					Expect(receivedConfig.EnableGrafanaLogs).To(BeTrue())
					Expect(analysis.Failures).To(HaveLen(2))
					Expect(analysis.Failures[0].Name).To(Equal("uploads file"))
					Expect(analysis.Failures[1].Name).To(Equal("button color"))
					return []GrafanaLogPlannedQuery{{
						FailureRef:    "f1",
						TestName:      "uploads file",
						BackendArea:   "file-storage",
						ExpectedError: "POST /api/storage returned 500 for claim-123",
						SearchTerms:   []string{"claim-123", "file-storage", "500"},
						LogQL:         `{namespace=~".+"} |~ "(?i)(claim-123|file-storage|500)"`,
						Reason:        "The UI file upload failed after a backend storage API 500.",
						Confidence:    "medium",
					}}, nil
				}
				runAIAnalysis = func(_ context.Context, receivedConfig Config, analysis Analysis) (*AIAnalysis, error) {
					Expect(receivedConfig.EnableAIAnalysis).To(BeTrue())
					Expect(analysis.GrafanaLogs).NotTo(BeNil())
					Expect(analysis.GrafanaLogs.Contexts).To(HaveLen(1))
					logContext := analysis.GrafanaLogs.Contexts[0]
					Expect(logContext.QueryLabel).To(Equal("AI-planned backend query"))
					Expect(logContext.Reason).To(ContainSubstring("storage API 500"))
					Expect(logContext.FailureRef).To(Equal("f1"))
					Expect(logContext.TestName).To(Equal("uploads file"))
					Expect(logContext.BackendArea).To(Equal("file-storage"))
					Expect(logContext.ExpectedError).To(Equal("POST /api/storage returned 500 for claim-123"))
					Expect(logContext.SearchTerms).To(ConsistOf("claim-123", "file-storage", "500"))
					Expect(logContext.Confidence).To(Equal("medium"))
					Expect(logContext.GrafanaExploreURL).To(ContainSubstring("/explore?"))
					Expect(logContext.Test).NotTo(BeNil())
					Expect(logContext.Test.Name).To(Equal("uploads file"))
					Expect(logContext.Entries).To(HaveLen(1))
					Expect(logContext.Entries[0].Line).To(ContainSubstring("claim-123"))
					Expect(logContext.Entries[0].Labels).To(HaveKeyWithValue("namespace", "file-storage"))
					return &AIAnalysis{
						StepSummary:  "## Test Failure Analysis\n\nAI report used Grafana backend evidence for file storage.",
						SlackSummary: "- *File storage* (infra/external): backend 500 evidence found in Grafana logs.\n- *Action:* Use the GitHub build summary for test-level failure reasons; inspect file-storage API logs.",
					}, nil
				}
				DeferCleanup(func() {
					runGrafanaLogQueryPlanning = previousPlanner
					runAIAnalysis = previousRunner
				})

				config.PreviousResultsPath = ""
				config.CompareWithPrevious = false
				config.EnableAIAnalysis = true
				config.ClaudeToken = "test-claude-token"
				config.EnableGrafanaLogs = true
				config.GrafanaURL = "https://grafana.example.com"
				config.GrafanaMCPEndpoint = mcpServer.URL + "/mcp"
				config.GrafanaLogStart = "2026-06-01T13:00:00Z"
				config.GrafanaLogEnd = "2026-06-01T14:00:00Z"
				config.GrafanaLogLimit = 5
				config.GrafanaLogMaxFailures = 2

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())
				Expect(queryCount.Load()).To(Equal(int32(1)))
				Expect(executedLogQL).To(Equal(`{namespace=~".+"} |~ "(?i)(claim-123|file-storage|500)"`))

				summary := readTestFile(summaryPath)
				Expect(summary).To(ContainSubstring("### Grafana Observations"))
				Expect(summary).To(ContainSubstring("uploads file"))
				Expect(summary).To(ContainSubstring("file-storage"))
				Expect(summary).To(ContainSubstring("1 matching log line returned"))
				Expect(summary).To(ContainSubstring("components: file-storage"))
				Expect(summary).To(ContainSubstring("[Open Grafana]"))
				Expect(summary).To(ContainSubstring("/explore?"))
				Expect(summary).To(ContainSubstring("panes="))
				Expect(summary).To(ContainSubstring("AI report used Grafana backend evidence for file storage."))
				Expect(summary).NotTo(ContainSubstring("### Grafana Log Context"))
				Expect(summary).NotTo(ContainSubstring("Exact failure error: `POST /api/storage returned 500 for claim-123`"))
				Expect(summary).NotTo(ContainSubstring(`{namespace=~".+"}`))
				Expect(summary).NotTo(ContainSubstring("file-storage controller failed claim-123 with backend 500"))
				Expect(summary).NotTo(ContainSubstring("### Failed Tests"))

				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("failed", "2"))
				Expect(outputs).To(HaveKeyWithValue("slack-sent", "false"))
			})

			It("should still run AI analysis when planned Grafana queries have no MCP endpoint", func() {
				planPath := filepath.Join(dir, "grafana-plan.json")
				Expect(writeGrafanaLogQueryPlan(planPath, []GrafanaLogPlannedQuery{{
					FailureRef:    "f1",
					TestName:      "creates instance",
					BackendArea:   "compute",
					ExpectedError: "Expected button to be visible",
					SearchTerms:   []string{"instance"},
					LogQL:         `{namespace=~".+"} |= "instance"`,
					Reason:        "Backend-shaped failure selected before MCP setup failed.",
					Confidence:    "medium",
				}})).To(Succeed())

				var aiCalled atomic.Bool
				previousRunner := runAIAnalysis
				runAIAnalysis = func(_ context.Context, receivedConfig Config, analysis Analysis) (*AIAnalysis, error) {
					aiCalled.Store(true)
					Expect(receivedConfig.EnableAIAnalysis).To(BeTrue())
					Expect(receivedConfig.EnableGrafanaLogs).To(BeTrue())
					Expect(analysis.Failures).To(HaveLen(1))
					Expect(analysis.GrafanaLogs).To(BeNil())
					return &AIAnalysis{
						StepSummary:  "## Test Failure Analysis\n\nAI fallback used test artifacts without Grafana logs.",
						SlackSummary: "- *Compute* (unknown): AI fallback used test artifacts without Grafana logs.",
					}, nil
				}
				DeferCleanup(func() {
					runAIAnalysis = previousRunner
				})

				config.EnableAIAnalysis = true
				config.ClaudeToken = "test-claude-token"
				config.EnableGrafanaLogs = true
				config.GrafanaQueryPlanPath = planPath
				config.GrafanaMCPEndpoint = ""

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())
				Expect(aiCalled.Load()).To(BeTrue())
				summary := readTestFile(summaryPath)
				Expect(summary).To(ContainSubstring("AI fallback used test artifacts without Grafana logs."))
				Expect(summary).NotTo(ContainSubstring("### Grafana Observations"))
				Expect(summary).NotTo(ContainSubstring(`{namespace=~".+"}`))
			})

			It("should continue when previous results cannot be parsed", func() {
				writeTestFile(previousPath, `not xml`)

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())
				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("new-failures", "0"))
				Expect(outputs).To(HaveKeyWithValue("recurring-failures", "0"))
				Expect(outputs).To(HaveKeyWithValue("resolved-failures", "0"))
			})

			It("should fail open when Slack notification fails by default", func() {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "slack unavailable", http.StatusInternalServerError)
				}))
				defer server.Close()

				config.SendSlack = true
				config.SlackWebhookURL = server.URL
				config.FailOnSlackError = false

				err := run(context.Background(), config)

				Expect(err).NotTo(HaveOccurred())
				outputs := readOutputFile(outputPath)
				Expect(outputs).To(HaveKeyWithValue("slack-sent", "false"))
			})

			It("should fail closed when fail-on-slack-error is enabled", func() {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "slack unavailable", http.StatusInternalServerError)
				}))
				defer server.Close()

				config.SendSlack = true
				config.SlackWebhookURL = server.URL
				config.FailOnSlackError = true

				err := run(context.Background(), config)

				Expect(err).To(MatchError(And(
					ContainSubstring("status 500"),
					ContainSubstring("slack unavailable"),
				)))
			})
		})
	})

	Context("When loading and validating configuration", func() {
		Describe("Given GitHub Actions environment variables", func() {
			It("should derive defaults for Slack, comparison, and workflow URL", func() {
				config := configFromEnv(map[string]string{
					"INPUT_TEST_RESULTS_PATH":     "results.xml",
					"INPUT_PREVIOUS_RESULTS_PATH": "previous.xml",
					"INPUT_SEND_SLACK":            "auto",
					"INPUT_SLACK_WEBHOOK_URL":     "https://hooks.slack.example/test",
					"INPUT_COMPARE_WITH_PREVIOUS": "auto",
					"INPUT_FORMAT":                "junit",
					"GITHUB_SERVER_URL":           "https://github.example",
					"GITHUB_REPOSITORY":           "nscaledev/quality-tooling",
					"GITHUB_RUN_ID":               "12345",
					"GITHUB_REF_NAME":             "feat/report",
					"GITHUB_ACTOR":                "octocat",
				})

				Expect(config.SendSlack).To(BeTrue())
				Expect(config.CompareWithPrevious).To(BeTrue())
				Expect(config.PreviousResultsFormat).To(Equal("junit"))
				Expect(config.WorkflowURL).To(Equal("https://github.example/nscaledev/quality-tooling/actions/runs/12345"))
				Expect(config.Branch).To(Equal("feat/report"))
				Expect(config.Actor).To(Equal("octocat"))
			})

			It("should parse process environment entries into a typed environment map", func() {
				env := envMapFromList([]string{
					"INPUT_TEST_RESULTS_PATH=results.xml",
					"INPUT_FORMAT=junit",
					"INVALID_ENTRY",
					"INPUT_TITLE=API=Results",
				})

				Expect(env).To(HaveKeyWithValue("INPUT_TEST_RESULTS_PATH", "results.xml"))
				Expect(env).To(HaveKeyWithValue("INPUT_FORMAT", "junit"))
				Expect(env).To(HaveKeyWithValue("INPUT_TITLE", "API=Results"))
				Expect(env).NotTo(HaveKey("INVALID_ENTRY"))
			})
		})

		Describe("Given invalid settings", func() {
			It("should reject missing test results path", func() {
				Expect((Config{}).validate()).To(MatchError(ContainSubstring("test-results-path is required")))
			})

			It("should reject Slack mode without usable credentials", func() {
				config := Config{TestResultsPath: "results.xml", SendSlack: true}

				Expect(config.validate()).To(MatchError(ContainSubstring("slack-webhook-url was not provided")))
			})

			It("should reject unsupported previous result sources", func() {
				config := Config{
					TestResultsPath:       "results.xml",
					CompareWithPrevious:   true,
					PreviousResultsSource: "artifact",
				}

				Expect(config.validate()).To(MatchError(ContainSubstring(`previous-results-source "artifact" is not supported`)))
			})
		})
	})

	Context("When resolving result files from a directory", func() {
		Describe("Given both generic and Ginkgo-style JSON result filenames exist", func() {
			var (
				dir             string
				resultsPath     string
				testResultsPath string
			)

			BeforeEach(func() {
				dir = GinkgoT().TempDir()
				baseTime := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
				resultsPath = filepath.Join(dir, "results.json")
				testResultsPath = filepath.Join(dir, "test-results.json")
				writeTestFileWithModTime(resultsPath, `{"suites":[]}`, baseTime)
				writeTestFileWithModTime(testResultsPath, `[{"SpecReports":[]}]`, baseTime.Add(time.Hour))
			})

			It("should prefer the canonical Playwright filename over a newer fallback", func() {
				resolved, err := resolveResultsPath(dir, "playwright-json")

				Expect(err).NotTo(HaveOccurred())
				Expect(resolved).To(Equal(resultsPath))
			})

			It("should prefer the canonical Ginkgo filename", func() {
				resolved, err := resolveResultsPath(dir, "ginkgo-json")

				Expect(err).NotTo(HaveOccurred())
				Expect(resolved).To(Equal(testResultsPath))
			})
		})

		Describe("Given only the alternate JSON filename exists for a requested JSON format", func() {
			It("should accept test-results.json for Playwright directory resolution", func() {
				dir := GinkgoT().TempDir()
				testResultsPath := filepath.Join(dir, "test-results.json")
				writeTestFileWithModTime(testResultsPath, `{"suites":[]}`, time.Now())

				resolved, err := resolveResultsPath(dir, "playwright-json")

				Expect(err).NotTo(HaveOccurred())
				Expect(resolved).To(Equal(testResultsPath))
			})

			It("should accept results.json for Ginkgo directory resolution", func() {
				dir := GinkgoT().TempDir()
				resultsPath := filepath.Join(dir, "results.json")
				writeTestFileWithModTime(resultsPath, `[{"SpecReports":[]}]`, time.Now())

				resolved, err := resolveResultsPath(dir, "ginkgo-json")

				Expect(err).NotTo(HaveOccurred())
				Expect(resolved).To(Equal(resultsPath))
			})
		})
	})

	Context("When preparing paths in the composite action", func() {
		Describe("Given raw path inputs are provided by GitHub", func() {
			var action string

			BeforeEach(func() {
				data, err := os.ReadFile("action.yml")
				Expect(err).NotTo(HaveOccurred())
				action = string(data)
			})

			It("should expand user-controlled paths from quoted environment variables", func() {
				Expect(action).To(ContainSubstring("INPUT_TEST_RESULTS_PATH_RAW: ${{ inputs.test-results-path }}"))
				Expect(action).To(ContainSubstring("INPUT_PREVIOUS_RESULTS_PATH_RAW: ${{ inputs.previous-results-path }}"))
				Expect(action).To(ContainSubstring(`TEST_RESULTS_PATH="${INPUT_TEST_RESULTS_PATH_RAW}"`))
				Expect(action).To(ContainSubstring(`PREVIOUS_RESULTS_PATH="${INPUT_PREVIOUS_RESULTS_PATH_RAW}"`))
			})

			It("should not interpolate raw GitHub input expressions inside the shell script", func() {
				Expect(action).NotTo(ContainSubstring(`TEST_RESULTS_PATH="${{ inputs.test-results-path }}"`))
				Expect(action).NotTo(ContainSubstring(`PREVIOUS_RESULTS_PATH="${{ inputs.previous-results-path }}"`))
			})

			It("should mask webhook and Claude token inputs through escaped workflow commands", func() {
				Expect(action).To(ContainSubstring(`mask_value "slack-webhook-url" "${INPUT_SLACK_WEBHOOK_URL:-}"`))
				Expect(action).To(ContainSubstring(`mask_value "claude-token" "${INPUT_CLAUDE_TOKEN:-}"`))
				Expect(action).To(ContainSubstring(`value="${value//%/%25}"`))
				Expect(action).NotTo(ContainSubstring(`echo "::add-mask::${INPUT_SLACK_WEBHOOK_URL}"`))
				Expect(action).NotTo(ContainSubstring(`echo "::add-mask::${INPUT_CLAUDE_TOKEN}"`))
			})

			It("should use the Teleport application tunnel for optional Grafana MCP enrichment", func() {
				Expect(action).To(ContainSubstring("enable-grafana-log-enrichment"))
				Expect(action).To(ContainSubstring("Resolve Grafana MCP inputs"))
				Expect(action).To(ContainSubstring("nks-dev-glo1-grafana"))
				Expect(action).To(ContainSubstring("nks-stg-europe-west2-grafana"))
				Expect(action).To(ContainSubstring("teleport-actions/application-tunnel@bb7a8fbfb67b85d26013554f10d71dd032c1c764"))
				Expect(action).To(ContainSubstring("token: ${{ inputs.grafana-teleport-token }}"))
				Expect(action).To(ContainSubstring("app: ${{ steps.grafana-resolve.outputs.grafana-app }}"))
				Expect(action).To(ContainSubstring("id: grafana-teleport-setup"))
				Expect(action).To(ContainSubstring("id: grafana-tunnel"))
				Expect(action).To(ContainSubstring("continue-on-error: true\n      uses: teleport-actions/setup@a820ebbf1bc1a496efca348ad21042d6e8df73a6"))
				Expect(action).To(ContainSubstring("continue-on-error: true\n      uses: teleport-actions/application-tunnel@bb7a8fbfb67b85d26013554f10d71dd032c1c764"))
				Expect(action).To(ContainSubstring("Warn Grafana Teleport setup failed"))
				Expect(action).To(ContainSubstring("Grafana Teleport tunnel setup failed; continuing without Grafana log enrichment so Claude can still analyze the test artifacts"))
				Expect(action).To(ContainSubstring("steps.grafana-teleport-setup.outcome != 'failure' && steps.grafana-tunnel.outcome != 'failure'"))
				Expect(action).To(ContainSubstring("mcp-grafana"))
				Expect(action).To(ContainSubstring(`write_output "grafana-mcp-endpoint" "http://127.0.0.1:${INPUT_GRAFANA_MCP_PORT}/mcp"`))
				Expect(action).To(ContainSubstring(`write_output "grafana-report-url" "${report_grafana_url}"`))
				Expect(action).To(ContainSubstring("GRAFANA_MCP_ENDPOINT: ${{ steps.grafana-start.outputs.grafana-mcp-endpoint }}"))
				Expect(action).To(ContainSubstring("GRAFANA_REPORT_URL: ${{ steps.grafana-start.outputs.grafana-report-url }}"))
				Expect(action).To(ContainSubstring("Grafana base URL for report links"))
				Expect(action).To(ContainSubstring("default: 'v0.7.10'"))
				Expect(action).To(ContainSubstring("grafana-mcp-version must be a pinned release tag"))
				Expect(action).To(ContainSubstring("warn_install_failed()"))
				Expect(action).To(ContainSubstring("continuing without Grafana log enrichment"))
				Expect(action).To(ContainSubstring(`warn_install_failed "download ${artifact} from grafana/mcp-grafana ${release} failed"`))
				Expect(action).To(ContainSubstring("mcp-grafana_${release#v}_checksums.txt"))
				Expect(action).To(ContainSubstring(`warn_install_failed "checksum mismatch for ${artifact}"`))
				Expect(action).NotTo(ContainSubstring("::error::Checksum mismatch for ${artifact}"))
				Expect(action).NotTo(ContainSubstring("GRAFANA_APP_RESOLVED=${grafana_app}"))
				Expect(action).NotTo(ContainSubstring("GRAFANA_SERVICE_ACCOUNT_TOKEN_RESOLVED=${grafana_token}"))
			})

			It("should use the test history OTLP writer bot when publishing needs the observability collector", func() {
				Expect(action).To(ContainSubstring("Resolve Test History publishing"))
				Expect(action).To(ContainSubstring("github-test-history-otlp-writer"))
				Expect(action).To(ContainSubstring("teleport-actions/auth-k8s@0f46164469ae4fcd4d359d40e06bab17d4be17c9"))
				Expect(action).To(ContainSubstring("token: ${{ inputs.test-history-teleport-token }}"))
				Expect(action).To(ContainSubstring("kubernetes-cluster: ${{ inputs.test-history-kube-cluster }}"))
				Expect(action).To(ContainSubstring(`kubectl port-forward`))
				Expect(action).To(ContainSubstring(`--address 127.0.0.1`))
				Expect(action).To(ContainSubstring(`"svc/${service}"`))
				Expect(action).To(ContainSubstring(`"${local_port}:${collector_port}"`))
				Expect(action).To(ContainSubstring("continue-on-error: true\n      shell: bash"))
				Expect(action).To(ContainSubstring(`INPUT_TEST_HISTORY_PUBLISH_MODE: ${{ steps.test-history-resolve.outputs.mode }}`))
				Expect(action).To(ContainSubstring(`INPUT_TEST_HISTORY_OTLP_ENDPOINT: ${{ steps.test-history-resolve.outputs.otlp-endpoint }}`))
				Expect(action).To(ContainSubstring("test-history-publish-mode"))
				Expect(action).To(ContainSubstring("Dump test history OTLP port-forward logs"))
			})

			It("should attach the test history retry spool when collector shipping fails", func() {
				Expect(action).To(ContainSubstring("test-history-upload-spool"))
				Expect(action).To(ContainSubstring("default: 'always'"))
				Expect(action).To(ContainSubstring("Upload test history retry spool"))
				Expect(action).To(ContainSubstring("actions/upload-artifact@v4"))
				Expect(action).To(ContainSubstring("test-history-upload-spool must be one of: on-failure, always, false"))
				Expect(action).To(ContainSubstring("write_output \"upload-spool\" \"$upload_spool\""))
				Expect(action).To(ContainSubstring("write_output \"spool-artifact-name\" \"$spool_artifact_name\""))
				Expect(action).To(ContainSubstring("steps.test-history-resolve.outputs.upload-spool == 'on-failure'"))
				Expect(action).To(ContainSubstring("steps.report.outputs.test-history-shipping-status == 'failed'"))
				Expect(action).To(ContainSubstring("name: ${{ steps.test-history-resolve.outputs.spool-artifact-name }}"))
				Expect(action).To(ContainSubstring("steps.report.outputs.test-history-spool-path"))
				Expect(action).To(ContainSubstring("include-hidden-files: true"))
				Expect(action).To(ContainSubstring("test-history-spool-artifact-url"))
				Expect(action).To(ContainSubstring("Append test history step summary"))
				Expect(action).To(ContainSubstring("### Test History"))
				Expect(action).To(ContainSubstring("wo11y-grafana-dev.nscale.teleport.sh/explore"))
				Expect(action).To(ContainSubstring("Test history data"))
				Expect(action).To(ContainSubstring("Test-history diagnostics"))
				Expect(action).To(ContainSubstring("open reporter logs for"))
				Expect(action).To(ContainSubstring("- Logs query:"))
				Expect(action).To(ContainSubstring("github_run_id=`{build_id}`"))
				Expect(action).NotTo(ContainSubstring("- Retry spool:"))
				Expect(action).NotTo(ContainSubstring("- wo11y Grafana:"))
				Expect(action).NotTo(ContainSubstring("- LogQL:"))
				Expect(action).NotTo(ContainSubstring(`|= "{build_id}"`))
				Expect(action).To(ContainSubstring("expr = f'{{service_name=\"test-results-report\"}} | github_run_id=`{build_id}`'"))
				Expect(action).To(ContainSubstring("TEST_HISTORY_SPOOL_ARTIFACT_URL: ${{ steps.test-history-upload-spool.outputs.artifact-url }}"))
			})

			It("should log Grafana MCP preflight decisions without exposing the service account token", func() {
				Expect(action).To(ContainSubstring("Grafana MCP enrichment preflight"))
				Expect(action).To(ContainSubstring(`mask_value "grafana-service-account-token" "$grafana_token"`))
				Expect(action).NotTo(ContainSubstring(`echo "::add-mask::${grafana_token}"`))
				Expect(action).To(ContainSubstring("Grafana service account token configured:"))
				Expect(action).To(ContainSubstring("Grafana org ID:"))
				Expect(action).To(ContainSubstring("MCP setup is deferred until Claude selects at least one backend-related Loki query."))
				Expect(action).To(ContainSubstring("candidate setup path: cannot start mcp-grafana if backend queries are planned; grafana-service-account-token is empty"))
				Expect(action).To(ContainSubstring("candidate setup path: open Teleport app tunnel"))
			})

			It("should plan Grafana MCP queries before starting local MCP infrastructure", func() {
				Expect(action).To(ContainSubstring("Plan Grafana MCP queries"))
				Expect(action).To(ContainSubstring("go run . --grafana-plan-only"))
				Expect(action).To(ContainSubstring("INPUT_GRAFANA_QUERY_PLAN_PATH: ${{ runner.temp }}/test-results-report-grafana-query-plan.json"))
				Expect(action).To(ContainSubstring("steps.grafana-plan.outputs.needs-mcp == 'true'"))
				Expect(action).To(ContainSubstring("INPUT_GRAFANA_QUERY_PLAN_PATH: ${{ steps.grafana-plan.outputs.plan-path }}"))
			})

			It("should use the Unikorn CR reader bot only after Claude plans CR lookups", func() {
				Expect(action).To(ContainSubstring("enable-unikorn-cr-enrichment"))
				Expect(action).To(ContainSubstring("github-unikorn-cr-reader"))
				Expect(action).To(ContainSubstring("Resolve Unikorn CR inputs"))
				Expect(action).To(ContainSubstring("nks-dev-glo1"))
				Expect(action).To(ContainSubstring("nks-stg-europe-west2"))
				Expect(action).To(ContainSubstring("Plan Unikorn CR queries"))
				Expect(action).To(ContainSubstring("go run . --unikorn-cr-plan-only"))
				Expect(action).To(ContainSubstring("steps.unikorn-cr-plan.outputs.needs-kube == 'true'"))
				Expect(action).To(ContainSubstring("teleport-actions/auth-k8s@0f46164469ae4fcd4d359d40e06bab17d4be17c9"))
				Expect(action).To(ContainSubstring("token: ${{ inputs.unikorn-cr-teleport-token }}"))
				Expect(action).To(ContainSubstring("kubernetes-cluster: ${{ steps.unikorn-cr-resolve.outputs.kube-cluster }}"))
				Expect(action).To(ContainSubstring("go run . --unikorn-cr-collect-only"))
				Expect(action).To(ContainSubstring("INPUT_UNIKORN_CR_CONTEXT_PATH: ${{ steps.unikorn-cr-collect.outputs.context-path }}"))
				Expect(action).To(ContainSubstring("kubectl auth is deferred until Claude selects at least one backend-related CR lookup."))
			})

			It("should not interpolate the action path directly into shell scripts", func() {
				Expect(action).To(ContainSubstring("ACTION_PATH: ${{ github.action_path }}"))
				Expect(action).To(ContainSubstring(`cd "${ACTION_PATH}"`))
				Expect(action).NotTo(ContainSubstring(`cd "${{ github.action_path }}"`))
			})

			It("should cache Go modules for the action dependencies", func() {
				Expect(action).To(ContainSubstring("cache: true"))
				Expect(action).To(ContainSubstring("cache-dependency-path: ${{ github.action_path }}/go.sum"))
			})

			It("should leave previous result format empty so it inherits the current format", func() {
				Expect(action).To(ContainSubstring("previous-results-format:"))
				Expect(action).To(ContainSubstring("Defaults to the current format"))
				Expect(action).To(ContainSubstring("default: ''"))
			})
		})
	})

	Context("When sending Slack notifications", func() {
		Describe("Given a lower-case report environment", func() {
			It("should render the environment in upper case in the title header", func() {
				payload := buildSlackPayload(Analysis{
					Current: TestRun{Name: "Region API Test Suites"},
					Stats:   Stats{Total: 1, Passed: 1},
				}, SlackOptions{
					Title:       "Region API Test Results",
					Environment: "dev",
				})

				rendered := slackPayloadText(payload)
				Expect(payload.Text).To(ContainSubstring("Region API Test Results (DEV) Region API Test Suites - Passed"))
				Expect(rendered).To(ContainSubstring("*Environment:*\n`dev`"))
			})
		})

		Describe("Given a Slack webhook", func() {
			It("should not send bearer authentication", func() {
				var (
					authHeader  string
					contentType string
				)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					authHeader = request.Header.Get("Authorization")
					contentType = request.Header.Get("Content-Type")
					w.WriteHeader(http.StatusOK)
				}))
				defer server.Close()

				err := sendSlack(context.Background(), Config{SlackWebhookURL: server.URL}, SlackPayload{Text: "hello"})

				Expect(err).NotTo(HaveOccurred())
				Expect(authHeader).To(BeEmpty())
				Expect(contentType).To(Equal("application/json"))
			})

			It("should include the response body when the webhook fails", func() {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "invalid webhook", http.StatusBadRequest)
				}))
				defer server.Close()

				err := sendSlack(context.Background(), Config{SlackWebhookURL: server.URL}, SlackPayload{Text: "hello"})

				Expect(err).To(MatchError(And(
					ContainSubstring("status 400"),
					ContainSubstring("invalid webhook"),
				)))
			})
		})
	})

	Context("When rendering input for AI analysis", func() {
		Describe("Given Claude is asked to write the report section", func() {
			It("should ask for pattern-level triage instead of repeated raw test lists", func() {
				prompt := claudePrompt()

				Expect(prompt).To(ContainSubstring("already includes run totals, links, environment details, actor information"))
				Expect(prompt).To(ContainSubstring("Evidence priority:"))
				Expect(prompt).To(ContainSubstring("Do not override suite-report evidence with lower-priority observations"))
				Expect(prompt).To(ContainSubstring("Confidence guidance:"))
				Expect(prompt).To(ContainSubstring("Reflect confidence through wording but do not add a confidence column"))
				Expect(prompt).To(ContainSubstring("Separate Failed Tests sections"))
				Expect(prompt).To(ContainSubstring("Group failures and skips by likely area or pattern"))
				Expect(prompt).To(ContainSubstring("Classification must be one of:"))
				Expect(prompt).To(ContainSubstring("- configuration"))
				Expect(prompt).To(ContainSubstring("All affected tests are skipped"))
				Expect(prompt).To(ContainSubstring("Other failures caused by test logic rather than product behavior"))
				Expect(prompt).To(ContainSubstring("Root cause cannot be confidently determined"))
				Expect(prompt).To(ContainSubstring("Use Grafana observations only when directly relevant and concrete"))
				Expect(prompt).To(ContainSubstring("Use CR observations only when directly relevant and concrete"))
				Expect(prompt).To(ContainSubstring("Use Grafana observations and CR observations only as supporting evidence"))
				Expect(prompt).To(ContainSubstring("Keep the report close to the existing production format"))
				Expect(prompt).To(ContainSubstring("Do not add a separate Grafana section"))
				Expect(prompt).To(ContainSubstring("Grafana showed INTERNAL_ERROR"))
				Expect(prompt).To(ContainSubstring("Network CR status phase=Error reason=VLANExhausted"))
				Expect(prompt).To(ContainSubstring("If a failed test time range and Grafana query time range are both present"))
				Expect(prompt).To(ContainSubstring("Do not claim an error occurred before the Grafana capture window unless the failed test began before that window"))
				Expect(prompt).To(ContainSubstring("provisioning/error signal was not present in the returned Grafana lines"))
				Expect(prompt).To(ContainSubstring("at most two test names per pattern"))
				Expect(prompt).To(ContainSubstring("Maximum 10 rows"))
				Expect(prompt).To(ContainSubstring("Group duplicate failure reasons into a single row"))
				Expect(prompt).To(ContainSubstring("4-6 high-signal Slack mrkdwn bullet lines"))
				Expect(prompt).To(ContainSubstring("*<suite/category>* (<category>):"))
				Expect(prompt).To(ContainSubstring("Each pattern bullet must explain"))
				Expect(prompt).To(ContainSubstring("Include Grafana only when it directly supports the failure interpretation"))
				Expect(prompt).To(ContainSubstring("Explicitly connect the test error, interpretation, and Grafana signal"))
				Expect(prompt).To(ContainSubstring("Explicitly connect the test error, interpretation, and CR signal"))
				Expect(prompt).To(ContainSubstring(`Do not use vague phrases like "Grafana returned related activity"`))
				Expect(prompt).To(ContainSubstring(`Do not use vague phrases like "CR state looked related"`))
				Expect(prompt).To(ContainSubstring("Do not mention Grafana merely to say evidence was time-disjoint"))
				Expect(prompt).To(ContainSubstring("Mention cleanup/audit/activity rows only when they directly match"))
				Expect(prompt).To(ContainSubstring(`Do not say "before the captured window" when the failed test start/end times are inside the Grafana query window`))
				Expect(prompt).To(ContainSubstring("Group by suite when one suite is affected"))
				Expect(prompt).To(ContainSubstring("Lead with the highest-attention product, infra, configuration, or environment blocker"))
				Expect(prompt).To(ContainSubstring("Keep temporary sentinel/test-validation failures short"))
				Expect(prompt).To(ContainSubstring("Do not include selector names, file paths, or retry details unless they materially change the next action"))
				Expect(prompt).To(ContainSubstring("Keep Slack as an overall summary by suite/failure category"))
				Expect(prompt).To(ContainSubstring("Do not add Evidence bullets"))
				Expect(prompt).To(ContainSubstring("disabled tests, pending tests, and sentinel skips"))
				Expect(prompt).To(ContainSubstring("removed or re-enabled"))
				Expect(prompt).To(ContainSubstring("intentional sentinel failures"))
				Expect(prompt).To(ContainSubstring("removed or disabled before review"))
				Expect(prompt).To(ContainSubstring("Do not mention issue alerting unless it appears in the evidence"))
				Expect(prompt).To(ContainSubstring("When failed tests are present"))
				Expect(prompt).To(ContainSubstring("Do not restate the test run title"))
				Expect(prompt).To(ContainSubstring("Finish with exactly one action bullet"))
				Expect(prompt).To(ContainSubstring("detailed test-level failure reasons are available in the GitHub build summary"))
				Expect(prompt).To(ContainSubstring("Do not mention test-level failure reasons for skip-only runs"))
			})

			It("should include a concrete compact example for step summary and Slack output", func() {
				prompt := claudePrompt()

				Expect(prompt).To(ContainSubstring("| Category | What failed | Why it failed | Likely reason | Impact | Next check |"))
				Expect(prompt).To(ContainSubstring("| configuration | Auth-dependent setup across suites"))
				Expect(prompt).To(ContainSubstring("### Representative Failed Tests"))
				Expect(prompt).To(ContainSubstring("| Suite / area | Representative tests | Failure reason | Count |"))
				Expect(prompt).To(ContainSubstring("| File Storage Management | attach storage, detach storage | HTTP 401 access_denied before product assertions | 8 |"))
				Expect(prompt).To(ContainSubstring("23 failed, 37 skipped"))
				Expect(prompt).To(ContainSubstring("- *Auth / all suites* (configuration): 23 setup-dependent tests failed with HTTP 401"))
				Expect(prompt).To(ContainSubstring("- *File Storage input validation* (skipped): 1 test is intentionally skipped for known bug INST-457"))
				Expect(prompt).To(ContainSubstring("- *File Storage attachment network* (infra/external): The test failed because network provisioning reached error instead of provisioned; Grafana showed vlan ids exhausted for the same resource during the test window"))
				Expect(prompt).NotTo(ContainSubstring("before the log capture window opened"))
				Expect(prompt).NotTo(ContainSubstring("- *Confidence:* High for the auth/config failure pattern"))
				Expect(prompt).NotTo(ContainSubstring("- *Details:* Test-level failure reasons are available in the GitHub build summary."))
				Expect(prompt).To(ContainSubstring("- *Action:* Use the GitHub build summary for detailed test-level failure reasons"))
			})
		})

		Describe("Given previous result comparison data is available", func() {
			var analysis Analysis

			BeforeEach(func() {
				analysis = Analysis{
					Current: TestRun{Name: "Console E2E"},
					Stats:   Stats{Passed: 4, Failed: 2, Skipped: 1},
					Failures: []TestCase{{
						ID:      "new-failure",
						Name:    "creates instance",
						Suite:   "compute.instance",
						File:    "src/spec/compute/instance.spec.ts",
						Line:    42,
						Message: "timeout",
					}},
					Skipped: []TestCase{{
						ID:      "new-skip",
						Name:    "deletes VPC",
						Suite:   "network.vpc",
						Message: "feature flag disabled",
					}},
					Compare: &Comparison{
						NewFailures: []TestCase{{
							ID:    "new-failure",
							Name:  "creates instance",
							Suite: "compute.instance",
							File:  "src/spec/compute/instance.spec.ts",
							Line:  42,
						}},
						RecurringFailures: []TestCase{{
							ID:    "recurring-failure",
							Name:  "updates instance",
							Suite: "compute.instance",
						}},
						ResolvedFailures: []TestCase{{
							ID:   "resolved-failure",
							Name: "lists instances",
						}},
						NewSkips: []TestCase{{
							ID:   "new-skip",
							Name: "deletes VPC",
						}},
						RecurringSkips: []TestCase{{
							ID:   "recurring-skip",
							Name: "creates VPC",
						}},
						ResolvedSkips: []TestCase{{
							ID:   "resolved-skip",
							Name: "updates VPC",
						}},
						PassedDelta:   2,
						FailedDelta:   1,
						SkippedDelta:  -1,
						DurationDelta: 2 * time.Second,
					},
				}
			})

			It("should include explicit new recurring and resolved labels", func() {
				input := renderAIInputWithOptions(analysis, AIInputOptions{})

				Expect(input).To(ContainSubstring("Previous result comparison:"))
				Expect(input).To(ContainSubstring("New failures: 1"))
				Expect(input).To(ContainSubstring("Recurring failures: 1"))
				Expect(input).To(ContainSubstring("Resolved failures: 1"))
				Expect(input).To(ContainSubstring("New skips: 1"))
				Expect(input).To(ContainSubstring("Recurring skips: 1"))
				Expect(input).To(ContainSubstring("Resolved skips: 1"))
				Expect(input).To(ContainSubstring("Passed delta: +2"))
				Expect(input).To(ContainSubstring("Failed delta: +1"))
				Expect(input).To(ContainSubstring("Skipped delta: -1"))
				Expect(input).To(ContainSubstring("Duration delta: +2.0s"))
			})

			It("should include comparison groups with test names suite names and locations", func() {
				input := renderAIInputWithOptions(analysis, AIInputOptions{})

				Expect(input).To(ContainSubstring("New failure tests:"))
				Expect(input).To(ContainSubstring("- creates instance [compute.instance] (src/spec/compute/instance.spec.ts:42)"))
				Expect(input).To(ContainSubstring("Recurring failure tests:"))
				Expect(input).To(ContainSubstring("- updates instance [compute.instance]"))
				Expect(input).To(ContainSubstring("Resolved failure tests:"))
				Expect(input).To(ContainSubstring("- lists instances"))
				Expect(input).To(ContainSubstring("New skipped tests:"))
				Expect(input).To(ContainSubstring("- deletes VPC"))
				Expect(input).To(ContainSubstring("Recurring skipped tests:"))
				Expect(input).To(ContainSubstring("- creates VPC"))
				Expect(input).To(ContainSubstring("Resolved skipped tests:"))
				Expect(input).To(ContainSubstring("- updates VPC"))
			})
		})

		Describe("Given many failures and skipped tests are present", func() {
			It("should cap the rendered AI input to the configured report limits", func() {
				analysis := Analysis{
					Current: TestRun{Name: "API Tests"},
					Stats:   Stats{Passed: 1, Failed: 4, Skipped: 3},
					Failures: []TestCase{
						{ID: "failure-1", Name: "failure one", Status: StatusFailed, Message: "one"},
						{ID: "failure-2", Name: "failure two", Status: StatusFailed, Message: "two"},
						{ID: "failure-3", Name: "failure three", Status: StatusFailed, Message: "three"},
						{ID: "failure-4", Name: "failure four", Status: StatusFailed, Message: "four"},
					},
					Skipped: []TestCase{
						{ID: "skip-1", Name: "skip one", Status: StatusSkipped, Message: "one"},
						{ID: "skip-2", Name: "skip two", Status: StatusSkipped, Message: "two"},
						{ID: "skip-3", Name: "skip three", Status: StatusSkipped, Message: "three"},
					},
					Compare: &Comparison{
						NewFailures: []TestCase{
							{ID: "new-failure-1", Name: "new failure one", Status: StatusFailed},
							{ID: "new-failure-2", Name: "new failure two", Status: StatusFailed},
							{ID: "new-failure-3", Name: "new failure three", Status: StatusFailed},
						},
						NewSkips: []TestCase{
							{ID: "new-skip-1", Name: "new skip one", Status: StatusSkipped},
							{ID: "new-skip-2", Name: "new skip two", Status: StatusSkipped},
						},
					},
				}

				input := renderAIInputWithOptions(analysis, AIInputOptions{MaxFailures: 2, MaxSkips: 1})

				Expect(input).To(ContainSubstring("Failed tests (showing first 2 of 4):"))
				Expect(input).To(ContainSubstring("Test: failure one"))
				Expect(input).To(ContainSubstring("Test: failure two"))
				Expect(input).To(ContainSubstring("2 additional failed tests omitted"))
				Expect(input).NotTo(ContainSubstring("failure three"))
				Expect(input).NotTo(ContainSubstring("failure four"))

				Expect(input).To(ContainSubstring("Skipped tests (showing first 1 of 3):"))
				Expect(input).To(ContainSubstring("Test: skip one"))
				Expect(input).To(ContainSubstring("2 additional skipped tests omitted"))
				Expect(input).NotTo(ContainSubstring("skip two"))
				Expect(input).NotTo(ContainSubstring("skip three"))

				Expect(input).To(ContainSubstring("New failure tests (showing first 2 of 3):"))
				Expect(input).To(ContainSubstring("- new failure one"))
				Expect(input).To(ContainSubstring("- new failure two"))
				Expect(input).To(ContainSubstring("- 1 additional tests omitted"))
				Expect(input).NotTo(ContainSubstring("new failure three"))

				Expect(input).To(ContainSubstring("New skipped tests (showing first 1 of 2):"))
				Expect(input).To(ContainSubstring("- new skip one"))
				Expect(input).NotTo(ContainSubstring("new skip two"))
			})
		})
	})

	Context("When rendering summaries with AI analysis", func() {
		Describe("Given the AI section will describe failure patterns", func() {
			It("should omit the raw failed and skipped test tables", func() {
				summary := renderStepSummary(Analysis{
					Current: TestRun{Name: "API Tests", Duration: 5 * time.Second},
					Stats:   Stats{Total: 4, Passed: 1, Failed: 2, Skipped: 1},
					Failures: []TestCase{{
						Name:    "should return flavors",
						Suite:   "Flavor Discovery",
						Message: "status code 401",
					}},
					Skipped: []TestCase{{
						Name:    "should list images",
						Suite:   "Image Discovery",
						Message: "skipped after setup failure",
					}},
				}, RenderOptions{
					Title:           "Region API Test Results",
					Environment:     "dev",
					WorkflowURL:     "https://github.example/run",
					IncludeSkips:    true,
					OmitTestDetails: true,
				})

				Expect(summary).To(ContainSubstring("## Region API Test Results"))
				Expect(summary).To(ContainSubstring("| Total | Passed | Failed | Skipped | Duration |"))
				Expect(summary).To(ContainSubstring("GitHub workflow run"))
				Expect(summary).NotTo(ContainSubstring("### Failed Tests"))
				Expect(summary).NotTo(ContainSubstring("### Skipped Tests"))
				Expect(summary).NotTo(ContainSubstring("should return flavors"))
				Expect(summary).NotTo(ContainSubstring("should list images"))
			})
		})
	})

	Context("When parsing AI analysis output", func() {
		It("should split step summary and Slack summary on the delimiter", func() {
			analysis := parseAIAnalysis("## Test Failure Analysis\nbody\n" + aiSlackDelimiter + "\nshort slack summary")

			Expect(analysis.StepSummary).To(Equal("## Test Failure Analysis\nbody"))
			Expect(analysis.SlackSummary).To(Equal("short slack summary"))
		})

		It("should ignore old or embedded delimiter text unless the configured delimiter is on its own line", func() {
			analysis := parseAIAnalysis("## Test Failure Analysis\nfailed test output:\n%%SLACK%%\nnot a delimiter\ninline " + aiSlackDelimiter + " text\n" + aiSlackDelimiter + "\n- *Action:* rerun")

			Expect(analysis.StepSummary).To(Equal("## Test Failure Analysis\nfailed test output:\n%%SLACK%%\nnot a delimiter\ninline " + aiSlackDelimiter + " text"))
			Expect(analysis.SlackSummary).To(Equal("- *Action:* rerun"))
		})

		It("should keep all output as step summary when no delimiter exists", func() {
			analysis := parseAIAnalysis("plain markdown only")

			Expect(analysis.StepSummary).To(Equal("plain markdown only"))
			Expect(analysis.SlackSummary).To(BeEmpty())
		})
	})

	Context("When Claude analysis is requested", func() {
		It("should skip without a token when there are no failures or skips", func() {
			analysis, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: true}, Analysis{})

			Expect(err).NotTo(HaveOccurred())
			Expect(analysis).To(BeNil())
		})

		It("should return a configuration error when failures exist and no token is set", func() {
			analysis, err := runClaudeAnalysis(context.Background(), Config{EnableAIAnalysis: true}, Analysis{
				Failures: []TestCase{{Name: "creates instance", Status: StatusFailed}},
			})

			Expect(analysis).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring("claude-token/CLAUDE_CODE_OAUTH_TOKEN is not set")))
		})
	})

	Context("When classifying Playwright statuses", func() {
		It("should use result statuses when top-level status is not decisive", func() {
			testCases := []struct {
				name string
				test playwrightTest
				want TestStatus
			}{
				{
					name: "failed result",
					test: playwrightTest{Results: []playwrightResult{{Status: "failed"}}},
					want: StatusFailed,
				},
				{
					name: "timed out result",
					test: playwrightTest{Results: []playwrightResult{{Status: "timedOut"}}},
					want: StatusFailed,
				},
				{
					name: "interrupted result",
					test: playwrightTest{Results: []playwrightResult{{Status: "interrupted"}}},
					want: StatusFailed,
				},
				{
					name: "skipped final result",
					test: playwrightTest{Results: []playwrightResult{{Status: "skipped"}}},
					want: StatusSkipped,
				},
				{
					name: "passed final result",
					test: playwrightTest{Results: []playwrightResult{{Status: "passed"}}},
					want: StatusPassed,
				},
				{
					name: "unknown status",
					test: playwrightTest{Status: "unknown"},
					want: StatusOther,
				},
			}

			for _, testCase := range testCases {
				GinkgoWriter.Printf("Checking Playwright status case: %s\n", testCase.name)
				Expect(playwrightStatus(testCase.test)).To(Equal(testCase.want), testCase.name)
			}
		})

		It("should classify result statuses case-insensitively", func() {
			Expect(normalizeStatus("timedOut")).To(Equal(StatusFailed))
			Expect(normalizeStatus("TIMEDOUT")).To(Equal(StatusFailed))
			Expect(normalizeStatus("FlAkY")).To(Equal(StatusPassed))
			Expect(normalizeStatus("PENDING")).To(Equal(StatusSkipped))
		})
	})

	Context("When detecting result formats", func() {
		It("should return useful errors for empty and unsupported input", func() {
			_, err := detectFormat([]byte("   "))
			Expect(err).To(MatchError(ContainSubstring("cannot detect empty test results")))

			_, err = detectFormat([]byte(`<report></report>`))
			Expect(err).To(MatchError(ContainSubstring("cannot detect XML test result format")))

			_, err = parseTestResults([]byte(`{"unknown":true}`), "auto")
			Expect(err).To(MatchError(ContainSubstring("cannot detect test result format")))

			_, err = parseTestResults([]byte(`{`), "auto")
			Expect(err).To(MatchError(ContainSubstring("cannot detect JSON test result format")))
		})
	})
})

func setEnv(key string, value string) {
	previous, existed := os.LookupEnv(key)
	Expect(os.Setenv(key, value)).To(Succeed())
	DeferCleanup(func() {
		if existed {
			Expect(os.Setenv(key, previous)).To(Succeed())
			return
		}
		Expect(os.Unsetenv(key)).To(Succeed())
	})
}

func writeTestFile(path string, contents string) {
	Expect(os.WriteFile(path, []byte(contents), 0o600)).To(Succeed())
}

func writeTestFileWithModTime(path string, contents string, modTime time.Time) {
	Expect(os.WriteFile(path, []byte(contents), 0o600)).To(Succeed())
	Expect(os.Chtimes(path, modTime, modTime)).To(Succeed())
}

func readTestFile(path string) string {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

func readOutputFile(path string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(readTestFile(path), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			result[key] = value
		}
	}
	return result
}
