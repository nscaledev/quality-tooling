package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The action owns a fixed default; only its explicit input changes the model,
// never the runner's ambient Claude configuration.
func TestClaudeModelConfiguration(t *testing.T) {
	for _, scenario := range []struct {
		name, input, ambient, want string
	}{
		{name: "default", want: "claude-sonnet-4-6"},
		{name: "blank input", input: " \t\n", want: "claude-sonnet-4-6"},
		{name: "explicit input", input: "claude-custom-model", want: "claude-custom-model"},
		{name: "trimmed input", input: " \tclaude-custom-model\n", want: "claude-custom-model"},
		{name: "ambient ignored", ambient: "claude-unavailable-model", want: "claude-sonnet-4-6"},
		{name: "explicit overrides ambient", input: "claude-custom-model", ambient: "claude-unavailable-model", want: "claude-custom-model"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := configFromEnv(map[string]string{
				"INPUT_CLAUDE_MODEL": scenario.input,
				"ANTHROPIC_MODEL":    scenario.ambient,
			})
			if config.ClaudeModel != scenario.want {
				t.Fatalf("configured model = %q, want %q", config.ClaudeModel, scenario.want)
			}
		})
	}
}

// All three real execution paths send the same explicit model argument, even
// when the process inherits a conflicting model setting.
func TestClaudeOperationsPassConfiguredModel(t *testing.T) {
	for _, scenario := range []struct{ name, input, want string }{
		{name: "default", want: "claude-sonnet-4-6"},
		{name: "override", input: "claude-custom-model", want: "claude-custom-model"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, runner := range diagnosticClaudeRunners() {
				t.Run(runner.name, func(t *testing.T) {
					dir := t.TempDir()
					argsPath := filepath.Join(dir, "args")
					script := "#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$TEST_MODEL_ARGS\"\ncat >/dev/null\nprintf '%s' '{\"queries\":[]}'\n"
					if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
					t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
					t.Setenv("TEST_MODEL_ARGS", argsPath)
					t.Setenv("ANTHROPIC_MODEL", "claude-unavailable-model")
					config := configFromEnv(map[string]string{"INPUT_CLAUDE_MODEL": scenario.input})
					config.EnableAIAnalysis = true
					config.ClaudeToken = "model-test-credential"
					if err := runner.run(context.Background(), config, diagnosticClaudeAnalysis()); err != nil {
						t.Fatalf("configured model execution failed: %v", err)
					}
					args := strings.Split(strings.TrimSuffix(readPlainTestFile(t, argsPath), "\x00"), "\x00")
					wantPrefix := []string{"--yes", "@anthropic-ai/claude-code@2.1.285", "--model", scenario.want, "-p"}
					if len(args) != len(wantPrefix)+1 || !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) || args[len(wantPrefix)] == "" {
						t.Fatalf("CLI argv = %q, want %q followed by one nonempty prompt", args, wantPrefix)
					}
				})
			}
		})
	}
}

// An unavailable explicitly selected model is visible and is not retried with
// a different model; optional AI failure preserves the real artifact's report.
func TestRunPreservesReportWhenConfiguredModelIsUnavailable(t *testing.T) {
	const model = "claude-unavailable-model"
	const rejection = "There's an issue with the selected model (claude-unavailable-model). It may not exist or you may not have access to it."
	dir := t.TempDir()
	callsPath := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf 'called\\n' >> \"$TEST_MODEL_CALLS\"\ncat >/dev/null\nprintf '%s' \"$TEST_MODEL_REJECTION\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_MODEL_CALLS", callsPath)
	t.Setenv("TEST_MODEL_REJECTION", rejection)
	outputsPath := filepath.Join(dir, "outputs")
	t.Setenv("GITHUB_OUTPUT", outputsPath)
	summaryPath := filepath.Join(dir, "summary.md")
	config := configFromEnv(map[string]string{
		"INPUT_TEST_RESULTS_PATH":  filepath.Join("testdata", "uni-region-junit.xml"),
		"INPUT_FORMAT":             formatJUnit,
		"GITHUB_STEP_SUMMARY":      summaryPath,
		"INPUT_ENABLE_AI_ANALYSIS": "true",
		"INPUT_CLAUDE_TOKEN":       "model-test-credential",
		"INPUT_CLAUDE_MODEL":       model,
	})
	logPath := filepath.Join(dir, "stderr.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = logFile
	t.Cleanup(func() { os.Stderr = originalStderr; _ = logFile.Close() })
	if err := run(context.Background(), config); err != nil {
		t.Fatalf("model rejection prevented normal reporting: %v", err)
	}
	if calls := readPlainTestFile(t, callsPath); calls != "called\n" {
		t.Fatalf("model rejection must not retry or fall back; CLI calls = %q", calls)
	}
	outputs := "\n" + readPlainTestFile(t, outputsPath)
	for _, expected := range []string{"total=3", "passed=1", "failed=1", "skipped=1", "conclusion=failure"} {
		if !strings.Contains(outputs, "\n"+expected+"\n") {
			t.Errorf("normal outputs missing %q: %s", expected, outputs)
		}
	}
	summary := readPlainTestFile(t, summaryPath)
	for _, expected := range []string{
		"**AI analysis:** failed.", "| 3 | 1 | 1 | 1 |", "### Failed Tests",
		"should delete the network resource", "expected 202, got 404", "### Skipped Tests",
		"should reject requests with invalid organization ID format",
		"Bug INST-457: File Storage API accepts invalid organizationId",
	} {
		if !strings.Contains(summary, expected) {
			t.Errorf("fallback summary missing %q: %s", expected, summary)
		}
	}
	logs := readPlainTestFile(t, logPath)
	for _, expected := range []string{model, "@anthropic-ai/claude-code@2.1.285", rejection, "exit status 1", "stderr=<empty>"} {
		if !strings.Contains(logs, expected) {
			t.Errorf("failure logs missing %q: %s", expected, logs)
		}
	}
	if strings.Count(logs, "::warning title=AI failure analysis failed::") != 1 || strings.Contains(logs, config.ClaudeToken) {
		t.Errorf("expected one credential-safe AI warning: %s", logs)
	}
	if strings.Contains(summary, rejection) || strings.Contains(summary, "**AI analysis:** completed.") {
		t.Errorf("model rejection must remain in logs and not count as completed analysis: %s", summary)
	}
}

// Caller-supplied model metadata is untrusted: both the startup log and returned
// error must redact credentials and keep embedded workflow commands inert.
func TestClaudeModelMetadataIsCredentialSafe(t *testing.T) {
	installDiagnosticClaude(t)
	t.Setenv("TEST_CLAUDE_STDOUT", "Model selection refused")
	t.Setenv("TEST_CLAUDE_STDERR", "")
	config := diagnosticClaudeConfig()
	config.ClaudeModel = "model-" + config.ClaudeToken + "\n::error::injected command"
	logPath := filepath.Join(t.TempDir(), "stderr.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = logFile
	t.Cleanup(func() { os.Stderr = originalStderr; _ = logFile.Close() })
	_, err = runClaudeAnalysis(context.Background(), config, diagnosticClaudeAnalysis())
	if err == nil {
		t.Fatal("expected rejected model")
	}
	logs := readPlainTestFile(t, logPath)
	for _, output := range []string{logs, err.Error()} {
		if strings.Contains(output, config.ClaudeToken) || strings.Contains(output, "\n::error::") {
			t.Errorf("model metadata leaked credentials or injected a workflow command: %q", output)
		}
		for _, expected := range []string{"model=", "[REDACTED]", `\n::error::injected command`, "@anthropic-ai/claude-code@2.1.285"} {
			if !strings.Contains(output, expected) {
				t.Errorf("safe metadata missing %q: %q", expected, output)
			}
		}
	}
	if strings.Count(logs, "\n") != 1 || strings.Contains(err.Error(), "\n") {
		t.Errorf("model metadata must remain single-line: logs=%q error=%q", logs, err)
	}
}

// Composite actions must forward the input independently to both planning
// processes and the final reporter, all using the documented default.
func TestActionWiresClaudeModelForEveryOperation(t *testing.T) {
	action := readPlainTestFile(t, "action.yml")
	inputBlock := regexp.MustCompile(`(?m)^  claude-model:\n(?:    .*\n)+`).FindString(action)
	if !strings.Contains(inputBlock, "    default: 'claude-sonnet-4-6'") {
		t.Fatalf("claude-model input must default to the fixed model: %s", inputBlock)
	}
	for _, step := range []string{"Plan Grafana MCP queries", "Plan Unikorn CR queries", "Generate Test Results Report"} {
		start := strings.Index(action, "    - name: "+step+"\n")
		if start < 0 {
			t.Errorf("missing action step %q", step)
			continue
		}
		block := strings.SplitN(action[start:], "\n    - name:", 2)[0]
		if !strings.Contains(block, "        INPUT_CLAUDE_MODEL: ${{ inputs.claude-model }}\n") {
			t.Errorf("step %q does not forward claude-model", step)
		}
	}
}
