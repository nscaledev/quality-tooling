package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stdout-only CLI failure must preserve failure and skip details when
// replaying the existing redacted uni-region fixture.
func TestRunReplaysRegionFixtureWhenClaudeFailsOnStdout(t *testing.T) {
	installDiagnosticClaude(t)
	t.Setenv("TEST_CLAUDE_STDOUT", "OAuth session rejected; log in again")
	t.Setenv("TEST_CLAUDE_STDERR", "")
	dir := t.TempDir()
	summaryPath := filepath.Join(dir, "summary.md")
	outputsPath := filepath.Join(dir, "outputs")
	logPath := filepath.Join(dir, "stderr.log")
	t.Setenv("GITHUB_OUTPUT", outputsPath)
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

	err = run(context.Background(), Config{
		TestResultsPath:  filepath.Join("testdata", "uni-region-junit.xml"),
		Format:           formatJUnit,
		WriteStepSummary: true,
		StepSummaryPath:  summaryPath,
		EnableAIAnalysis: true,
		ClaudeToken:      "replay-test-credential",
		MaxFailures:      10,
		MaxSkips:         10,
		IncludeSkips:     true,
	})
	if err != nil {
		t.Fatalf("optional AI failure prevented fixture reporting: %v", err)
	}

	outputs := "\n" + readPlainTestFile(t, outputsPath)
	for _, expected := range []string{"total=3", "passed=1", "failed=1", "skipped=1", "conclusion=failure"} {
		if !strings.Contains(outputs, "\n"+expected+"\n") {
			t.Errorf("fixture outputs missing %q: %s", expected, outputs)
		}
	}
	summary := readPlainTestFile(t, summaryPath)
	for _, expected := range []string{
		"**AI analysis:** failed.", "| 3 | 1 | 1 | 1 |",
		"### Failed Tests", "should delete the network resource", "expected 202, got 404",
		"### Skipped Tests", "should reject requests with invalid organization ID format",
		"Bug INST-457: File Storage API accepts invalid organizationId",
	} {
		if !strings.Contains(summary, expected) {
			t.Errorf("fixture summary missing %q: %s", expected, summary)
		}
	}
	logs := readPlainTestFile(t, logPath)
	if !strings.Contains(logs, `stdout="OAuth session rejected; log in again"; stderr=<empty>`) {
		t.Errorf("stdout-only CLI diagnostic missing from warning: %s", logs)
	}
	if strings.Contains(summary, "OAuth session rejected") {
		t.Error("raw CLI diagnostics belong in logs, not the report summary")
	}
}
