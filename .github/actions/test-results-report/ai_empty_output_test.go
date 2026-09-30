package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A zero exit status is successful analysis only when at least one parsed
// summary has content; absent content must leave the regular test report intact.
func TestClaudeSuccessfulExitRequiresAnalysisContent(t *testing.T) {
	const emptyOutputError = "run claude analysis: Claude exited successfully but returned no analysis content"
	const markdown = "## Test Failure Analysis\n\nInvestigate the network deletion response."
	const slack = "Network deletion returned an unexpected status."
	for _, scenario := range []struct {
		name   string
		output string
		want   *AIAnalysis
	}{
		{name: "empty"},
		{name: "whitespace only", output: " \t\r\n\u00a0\n"},
		{name: "delimiter only", output: aiSlackDelimiter},
		{name: "delimiter with whitespace", output: " \t\n" + aiSlackDelimiter + "\r\n \t\n"},
		{name: "markdown only", output: "\n" + markdown + "\n", want: &AIAnalysis{StepSummary: markdown}},
		{name: "split summaries", output: markdown + "\n" + aiSlackDelimiter + "\n" + slack + "\n", want: &AIAnalysis{StepSummary: markdown, SlackSummary: slack}},
		{name: "slack only", output: "\n" + aiSlackDelimiter + "\n" + slack + "\n", want: &AIAnalysis{SlackSummary: slack}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			// Mock only the external CLI: parsing, analysis, reporting, and output
			// publication all execute their real implementation below.
			script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' \"$TEST_CLAUDE_SUCCESS_STDOUT\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_CLAUDE_SUCCESS_STDOUT", scenario.output)
			outputsPath := filepath.Join(dir, "outputs")
			t.Setenv("GITHUB_OUTPUT", outputsPath)
			summaryPath := filepath.Join(dir, "summary.md")
			config := Config{
				TestResultsPath:  filepath.Join("testdata", "uni-region-junit.xml"),
				Format:           formatJUnit,
				WriteStepSummary: true,
				StepSummaryPath:  summaryPath,
				EnableAIAnalysis: true,
				ClaudeToken:      "empty-output-test-credential",
				MaxFailures:      10,
				MaxSkips:         10,
				IncludeSkips:     true,
			}
			current, err := readAndParse(config.TestResultsPath, config.Format)
			if err != nil {
				t.Fatal(err)
			}
			got, err := runClaudeAnalysis(context.Background(), config, analyze(current, nil))
			if scenario.want == nil {
				if got != nil || err == nil || err.Error() != emptyOutputError {
					t.Errorf("empty successful output must return nil and descriptive error; got %#v, %v", got, err)
				}
			} else if err != nil || got == nil || *got != *scenario.want {
				t.Errorf("valid output = %#v, %v; want %#v without error", got, err, scenario.want)
			}

			logPath := filepath.Join(dir, "stderr.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			originalStderr := os.Stderr
			os.Stderr = logFile
			t.Cleanup(func() {
				os.Stderr = originalStderr
				_ = logFile.Close()
			})
			if err := run(context.Background(), config); err != nil {
				t.Fatalf("optional AI outcome must preserve reporting: %v", err)
			}
			outputs := "\n" + readPlainTestFile(t, outputsPath)
			for _, expected := range []string{"total=3", "passed=1", "failed=1", "skipped=1", "conclusion=failure"} {
				if !strings.Contains(outputs, "\n"+expected+"\n") {
					t.Errorf("normal test outputs missing %q: %s", expected, outputs)
				}
			}
			summary := readPlainTestFile(t, summaryPath)
			logs := readPlainTestFile(t, logPath)
			if scenario.want == nil {
				for _, expected := range []string{
					"**AI analysis:** failed.", "| 3 | 1 | 1 | 1 |",
					"### Failed Tests", "should delete the network resource", "expected 202, got 404",
					"### Skipped Tests", "should reject requests with invalid organization ID format",
					"Bug INST-457: File Storage API accepts invalid organizationId",
				} {
					if !strings.Contains(summary, expected) {
						t.Errorf("fallback summary missing %q: %s", expected, summary)
					}
				}
				if strings.Contains(summary, "**AI analysis:** completed.") {
					t.Error("empty analysis was reported as completed")
				}
				if strings.Count(logs, "::warning title=") != 1 || !strings.Contains(logs, "::warning title=AI failure analysis failed::"+emptyOutputError+"\n") {
					t.Errorf("expected exactly one useful AI failure warning: %s", logs)
				}
			} else {
				if !strings.Contains(summary, "**AI analysis:** completed.") || strings.Contains(summary, "**AI analysis:** failed.") {
					t.Errorf("valid content must report completed analysis: %s", summary)
				}
				if strings.Contains(logs, "::warning title=AI failure analysis failed::") {
					t.Errorf("valid content produced a failure warning: %s", logs)
				}
				if scenario.want.StepSummary != "" && !strings.Contains(summary, scenario.want.StepSummary) {
					t.Errorf("valid Markdown analysis missing from summary: %s", summary)
				}
			}
		})
	}
}
