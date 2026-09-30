package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Each public Claude execution path must retain stdout-only failures, preserve
// the exit code, and identify an empty stream instead of producing a blank error.
func TestClaudeFailuresRetainBothDiagnosticStreams(t *testing.T) {
	installDiagnosticClaude(t)
	for _, runner := range diagnosticClaudeRunners() {
		t.Run(runner.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name, stdout, stderr, want string
			}{
				{"stdout", "Authentication expired", "", `stdout="Authentication expired"; stderr=<empty>`},
				{"stderr", "", "Network unavailable", `stdout=<empty>; stderr="Network unavailable"`},
				{"both", "Login required", "Credential rejected", `stdout="Login required"; stderr="Credential rejected"`},
				{"empty", "", "", `stdout=<empty>; stderr=<empty>`},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					t.Setenv("TEST_CLAUDE_STDOUT", scenario.stdout)
					t.Setenv("TEST_CLAUDE_STDERR", scenario.stderr)
					err := runner.run(context.Background(), diagnosticClaudeConfig(), diagnosticClaudeAnalysis())
					var exitError *exec.ExitError
					if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
						t.Fatalf("expected wrapped exit status 1, got %v", err)
					}
					if !strings.Contains(err.Error(), scenario.want) {
						t.Fatalf("expected %q, got %v", scenario.want, err)
					}
					if errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("ordinary CLI failure was classified as timeout: %v", err)
					}
				})
			}
		})
	}
}

// Redaction applies before truncation so even credentials spanning the output
// limit cannot leak prefixes, and terminal/log control characters stay inert.
func TestClaudeFailureDiagnosticsRedactCredentialsAndBoundOutput(t *testing.T) {
	installDiagnosticClaude(t)
	config := diagnosticClaudeConfig()
	config.SlackWebhookURL = "https://hooks.example.test/private-path"
	config.ComponentVersionToken = "component-secret-value"
	config.TestHistoryToken = "history-secret-value"
	secrets := []string{
		config.ClaudeToken, config.SlackWebhookURL, config.ComponentVersionToken, config.TestHistoryToken,
		"sk-ant-oat01-synthetic", "ghp_syntheticGithubSecret", "github_pat_syntheticPat",
		"eyJhbGciOiJub25lIn0.eyJzdWIiOiJ0ZXN0In0.signature",
		"arbitraryBearerSecret", "basicCredentialValue", "jsonSecretValue", "querySecretValue",
		"urlPassword", "xoxb-synthetic-token", "AKIA1234567890ABCDEF",
		"https://hooks.slack.com/services/TTEST/BTEST/syntheticSecret",
	}
	output := "Credentials rejected: " + strings.Join(secrets[:8], " ") +
		" Bearer arbitraryBearerSecret Basic basicCredentialValue" +
		` {"api_key":"jsonSecretValue"} access_token=querySecretValue` +
		" https://user:urlPassword@example.test " + strings.Join(secrets[13:], " ") +
		"\n::error::untrusted output\r\x1b[31mterminal color\x1b[0m"
	t.Setenv("TEST_CLAUDE_STDOUT", output)
	t.Setenv("TEST_CLAUDE_NUL", "true")
	t.Setenv("TEST_CLAUDE_STDERR", strings.Repeat("x", claudeDiagnosticLimit-5)+config.ClaudeToken+strings.Repeat("y", claudeDiagnosticLimit))
	_, err := runClaudeAnalysis(context.Background(), config, diagnosticClaudeAnalysis())
	if err == nil {
		t.Fatal("expected failed CLI")
	}
	message := err.Error()
	for _, secret := range secrets {
		if strings.Contains(message, secret) {
			t.Fatalf("diagnostic exposed credential %q", secret)
		}
	}
	for _, control := range []string{"\n", "\r", "\x1b", "\x00"} {
		if strings.Contains(message, control) {
			t.Fatalf("diagnostic contains raw control character %q", control)
		}
	}
	if !strings.Contains(message, `\n::error::untrusted output\rterminal color\x00`) {
		t.Fatalf("diagnostic did not safely retain multiline context: %s", message)
	}
	if !strings.Contains(message, "[REDACTED]") || !strings.Contains(message, "[truncated]") {
		t.Fatalf("expected redaction and truncation markers: %s", message)
	}
	if len(message) > 2*claudeDiagnosticLimit+256 {
		t.Fatalf("diagnostic exceeded stream bounds: %d bytes", len(message))
	}
}

// URL passwords may contain @. Redact through the last authority delimiter,
// while retaining the host and @ characters in paths, queries, and fragments.
func TestClaudeDiagnosticsRedactCompleteURLCredentials(t *testing.T) {
	for _, scenario := range []struct {
		name, input, want string
	}{
		{"password with at sign", "https://user:p@secret-suffix@example.test/resource@path", "https://[REDACTED]@example.test/resource@path"},
		{"query boundary", "https://user:secret@example.test?identity=name@example.test", "https://[REDACTED]@example.test?identity=name@example.test"},
		{"fragment boundary", "https://user:secret@example.test#name@example.test", "https://[REDACTED]@example.test#name@example.test"},
		{"no credentials", "https://example.test/resource@path?identity=name@example.test#name@example.test", "https://example.test/resource@path?identity=name@example.test#name@example.test"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := claudeDiagnostic("Connection refused for "+scenario.input, Config{})
			want := strconv.Quote("Connection refused for " + scenario.want)
			if got != want {
				t.Fatalf("diagnostic = %s, want %s", got, want)
			}
		})
	}
}

// Escaped quotes and backslashes belong to the credential value, so no suffix
// survives redaction and the following diagnostic message remains readable.
func TestClaudeDiagnosticsRedactEscapedQuotedCredentials(t *testing.T) {
	for _, scenario := range []struct {
		name, input, want string
	}{
		{"escaped JSON quote", `{"password":"secret-prefix\"secret-suffix","message":"authentication rejected"}`, `{"password":[REDACTED],"message":"authentication rejected"}`},
		{"escaped JSON backslash and quote", `{"password":"secret-prefix\\\"secret-suffix","message":"authentication rejected"}`, `{"password":[REDACTED],"message":"authentication rejected"}`},
		{"terminal escaped JSON backslash", `{"password":"secret-prefix\\","message":"authentication rejected"}`, `{"password":[REDACTED],"message":"authentication rejected"}`},
		{"escaped single quote", `password='secret-prefix\'secret-suffix' authentication rejected`, `password=[REDACTED] authentication rejected`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := claudeDiagnostic(scenario.input, Config{})
			if want := strconv.Quote(scenario.want); got != want {
				t.Fatalf("diagnostic = %s, want %s", got, want)
			}
		})
	}
}

// Timeout errors retain diagnostics and remain distinguishable from exit status
// failures for analysis and both planners, without extending their deadlines.
func TestClaudeTimeoutDiagnosticsPreserveDeadlineCause(t *testing.T) {
	installDiagnosticClaude(t)
	t.Setenv("TEST_CLAUDE_STDOUT", "Waiting for upstream")
	t.Setenv("TEST_CLAUDE_STDERR", "")
	t.Setenv("TEST_CLAUDE_SLEEP", "true")
	for _, runner := range diagnosticClaudeRunners() {
		t.Run(runner.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			started := time.Now()
			err := runner.run(ctx, diagnosticClaudeConfig(), diagnosticClaudeAnalysis())
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after") {
				t.Fatalf("expected wrapped deadline failure, got %v", err)
			}
			if !strings.Contains(err.Error(), `stdout="Waiting for upstream"; stderr=<empty>`) {
				t.Fatalf("timeout lost CLI diagnostics: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("timeout failed to terminate CLI promptly: %s", elapsed)
			}
		})
	}
}

// The default remains five minutes; obtaining diagnostics never changes the
// externally configured time budget.
func TestClaudeAnalysisRetainsFiveMinuteDefault(t *testing.T) {
	if config := configFromEnv(map[string]string{}); config.AIAnalysisTimeout != 300*time.Second {
		t.Fatalf("default AI timeout = %s, want 300s", config.AIAnalysisTimeout)
	}
}

type diagnosticClaudeRunner struct {
	name string
	run  func(context.Context, Config, Analysis) error
}

func diagnosticClaudeRunners() []diagnosticClaudeRunner {
	return []diagnosticClaudeRunner{
		{"analysis", func(ctx context.Context, config Config, analysis Analysis) error {
			_, err := runClaudeAnalysis(ctx, config, analysis)
			return err
		}},
		{"grafana", func(ctx context.Context, config Config, analysis Analysis) error {
			_, err := runClaudeGrafanaLogQueryPlanning(ctx, config, analysis)
			return err
		}},
		{"unikorn", func(ctx context.Context, config Config, analysis Analysis) error {
			_, err := runClaudeUnikornCRQueryPlanning(ctx, config, analysis)
			return err
		}},
	}
}

func diagnosticClaudeConfig() Config {
	return Config{EnableAIAnalysis: true, ClaudeToken: "configured-opaque-credential", MaxFailures: 1}
}

func diagnosticClaudeAnalysis() Analysis {
	return Analysis{Failures: []TestCase{{Name: "failed test", Message: "POST returned 500"}}}
}

func installDiagnosticClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s' "${TEST_CLAUDE_STDOUT:-}"
printf '%s' "${TEST_CLAUDE_STDERR:-}" >&2
if [ "${TEST_CLAUDE_NUL:-}" = true ]; then
  printf '\000'
fi
cat >/dev/null
if [ "${TEST_CLAUDE_SLEEP:-}" = true ]; then
  exec sleep 5
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_CLAUDE_SLEEP", "false")
	t.Setenv("TEST_CLAUDE_NUL", "false")
}
