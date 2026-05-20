package main

import (
	"os"
	"path/filepath"
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

	Context("When rendering input for AI analysis", func() {
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
})

func writeTestFileWithModTime(path string, contents string, modTime time.Time) {
	Expect(os.WriteFile(path, []byte(contents), 0o600)).To(Succeed())
	Expect(os.Chtimes(path, modTime, modTime)).To(Succeed())
}
