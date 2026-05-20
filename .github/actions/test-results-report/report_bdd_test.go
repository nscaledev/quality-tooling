package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

				GinkgoWriter.Printf("Report outputs: %+v\n", outputs)
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
					"INPUT_TEST_RESULTS_PATH":       "results.xml",
					"INPUT_PREVIOUS_RESULTS_PATH":   "previous.xml",
					"INPUT_SEND_SLACK":              "auto",
					"INPUT_SLACK_BOT_TOKEN":         "xoxb-token",
					"INPUT_SLACK_CHANNEL":           "C123",
					"INPUT_COMPARE_WITH_PREVIOUS":   "auto",
					"INPUT_PREVIOUS_RESULTS_FORMAT": "",
					"INPUT_FORMAT":                  "junit",
					"GITHUB_SERVER_URL":             "https://github.example",
					"GITHUB_REPOSITORY":             "nscaledev/quality-tooling",
					"GITHUB_RUN_ID":                 "12345",
					"GITHUB_REF_NAME":               "feat/report",
					"GITHUB_ACTOR":                  "octocat",
				})

				Expect(config.SendSlack).To(BeTrue())
				Expect(config.CompareWithPrevious).To(BeTrue())
				Expect(config.PreviousResultsFormat).To(Equal("junit"))
				Expect(config.WorkflowURL).To(Equal("https://github.example/nscaledev/quality-tooling/actions/runs/12345"))
				Expect(config.Branch).To(Equal("feat/report"))
				Expect(config.Actor).To(Equal("octocat"))
			})
		})

		Describe("Given invalid settings", func() {
			It("should reject missing test results path", func() {
				Expect((Config{}).validate()).To(MatchError(ContainSubstring("test-results-path is required")))
			})

			It("should reject Slack mode without usable credentials", func() {
				config := Config{TestResultsPath: "results.xml", SendSlack: true}

				Expect(config.validate()).To(MatchError(ContainSubstring("neither slack-webhook-url nor slack-bot-token + slack-channel")))
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

		Describe("Given a Slack bot token and channel", func() {
			It("should post JSON with bearer authentication and channel fallback", func() {
				var (
					received    SlackPayload
					method      string
					authHeader  string
					contentType string
					decodeErr   error
				)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					method = request.Method
					authHeader = request.Header.Get("Authorization")
					contentType = request.Header.Get("Content-Type")
					decodeErr = json.NewDecoder(request.Body).Decode(&received)

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				}))
				defer server.Close()

				config := Config{
					SlackBotToken: "xoxb-token",
					SlackChannel:  "C123",
					SlackAPIURL:   server.URL,
				}

				err := sendSlackBotMessage(context.Background(), config, SlackPayload{Text: "hello"})

				Expect(err).NotTo(HaveOccurred())
				Expect(method).To(Equal(http.MethodPost))
				Expect(authHeader).To(Equal("Bearer xoxb-token"))
				Expect(contentType).To(Equal("application/json"))
				Expect(decodeErr).NotTo(HaveOccurred())
				Expect(received.Channel).To(Equal("C123"))
				Expect(received.Text).To(Equal("hello"))
			})

			It("should fail when Slack returns ok=false", func() {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
				}))
				defer server.Close()

				config := Config{
					SlackBotToken: "xoxb-token",
					SlackChannel:  "C123",
					SlackAPIURL:   server.URL,
				}

				err := sendSlackBotMessage(context.Background(), config, SlackPayload{Text: "hello"})

				Expect(err).To(MatchError(ContainSubstring("channel_not_found")))
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

				err := sendSlackWebhook(context.Background(), server.URL, SlackPayload{Text: "hello"})

				Expect(err).NotTo(HaveOccurred())
				Expect(authHeader).To(BeEmpty())
				Expect(contentType).To(Equal("application/json"))
			})

			It("should include the response body when the webhook fails", func() {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "invalid webhook", http.StatusBadRequest)
				}))
				defer server.Close()

				err := sendSlackWebhook(context.Background(), server.URL, SlackPayload{Text: "hello"})

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

				Expect(prompt).To(ContainSubstring("already includes run totals, links, and any previous-result comparison"))
				Expect(prompt).To(ContainSubstring(`do not add separate "Failed Tests" or "Skipped Tests" sections`))
				Expect(prompt).To(ContainSubstring("Group failures and skips by likely area or pattern"))
				Expect(prompt).To(ContainSubstring("cap examples to 2 per row"))
				Expect(prompt).To(ContainSubstring("Each bullet must start with '- *<suite/category>:*'"))
				Expect(prompt).To(ContainSubstring("Group by suite name when one suite is affected"))
				Expect(prompt).To(ContainSubstring("test-level failure reasons can be found in the GitHub build summary"))
				Expect(prompt).To(ContainSubstring("Do not restate the test run title"))
			})

			It("should include a concrete compact example for step summary and Slack output", func() {
				prompt := claudePrompt()

				Expect(prompt).To(ContainSubstring("| Area / signal | Impact | Likely cause | Next check |"))
				Expect(prompt).To(ContainSubstring("Auth / 401 responses"))
				Expect(prompt).To(ContainSubstring("23 failed, 37 skipped"))
				Expect(prompt).To(ContainSubstring("- *Auth / all suites:* 23 failures and 37 skips"))
				Expect(prompt).To(ContainSubstring("- *Validation paths:* 3 negative-path tests"))
				Expect(prompt).To(ContainSubstring("- *Details:* Test-level failure reasons can be found in the GitHub build summary."))
				Expect(prompt).To(ContainSubstring("- *Next:* refresh the token or config"))
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
				input := renderAIInput(analysis)

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
				input := renderAIInput(analysis)

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
			analysis := parseAIAnalysis("## Test Failure Analysis\nbody\n%%SLACK%%\nshort slack summary")

			Expect(analysis.StepSummary).To(Equal("## Test Failure Analysis\nbody"))
			Expect(analysis.SlackSummary).To(Equal("short slack summary"))
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
