package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const claudeDiagnosticLimit = 2048

var (
	claudeANSI = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
	// Escaped quotes do not end credential values emitted in structured logs.
	claudeCredentialAssignment = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_-]*token|[a-z0-9_-]*secret|[a-z0-9_-]*password|api[_-]?key|authorization|_auth)["']?[ \t]*[:=][ \t]*)(?:"(?:\\[^\r\n]|[^"\\\r\n])*"|'(?:\\[^\r\n]|[^'\\\r\n])*'|[^\s,;]+)`)
	claudeAuthorization        = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9_./+=-]+`)
	claudeKnownToken           = regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9_-]+|gh[pousr]_[a-zA-Z0-9_]+|github_pat_[a-zA-Z0-9_]+|glpat-[a-zA-Z0-9_-]+|xox[baprs]-[a-zA-Z0-9-]+|(?:AKIA|ASIA)[A-Z0-9]{16}|eyJ[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]*){2,})`)
	// Use the final @ within the authority: a password may contain earlier @s.
	claudeURLCredentials = regexp.MustCompile(`(?i)(https?://)[^\s/?#"'<>]*@`)
	claudeSlackWebhook   = regexp.MustCompile(`https://hooks\.slack\.com/services/[^\s"'<>]+`)
)

func executeClaude(ctx context.Context, config Config, operation, prompt, input string) (string, error) {
	cmd := newClaudeCommand(ctx, config, prompt, input)
	// Model input is caller-controlled: use the same bounded redaction as CLI
	// output so diagnostic metadata cannot leak credentials or inject log lines.
	invocation := fmt.Sprintf("cli=%s; model=%s", claudeCodePackage, claudeDiagnostic(config.ClaudeModel, config))
	fmt.Fprintf(os.Stderr, "Claude invocation: operation=%q; %s\n", operation, invocation)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	if err := cmd.Run(); err != nil {
		// The CLI can report authentication and other failures on stdout. Keep
		// both streams, but redact credentials before exposing bounded diagnostics.
		diagnostic := fmt.Sprintf("stdout=%s; stderr=%s",
			claudeDiagnostic(stdout.String(), config), claudeDiagnostic(stderr.String(), config))
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s: timed out after %s (%w): %w; %s; %s", operation,
				time.Since(started).Round(time.Millisecond), context.DeadlineExceeded, err, invocation, diagnostic)
		}
		return "", fmt.Errorf("%s: %w; %s; %s", operation, err, invocation, diagnostic)
	}
	return stdout.String(), nil
}

func claudeDiagnostic(output string, config Config) string {
	output = claudeANSI.ReplaceAllString(output, "")
	for _, secret := range []string{config.ClaudeToken, config.SlackWebhookURL, config.ComponentVersionToken, config.TestHistoryToken} {
		if secret != "" {
			output = strings.ReplaceAll(output, secret, "[REDACTED]")
		}
	}
	output = claudeAuthorization.ReplaceAllString(output, "[REDACTED AUTHORIZATION]")
	output = claudeCredentialAssignment.ReplaceAllString(output, "${1}[REDACTED]")
	output = claudeKnownToken.ReplaceAllString(output, "[REDACTED]")
	output = claudeURLCredentials.ReplaceAllString(output, "${1}[REDACTED]@")
	output = claudeSlackWebhook.ReplaceAllString(output, "[REDACTED]")
	output = strings.TrimSpace(output)
	if output == "" {
		return "<empty>"
	}
	// Quote each rune before bounding: control characters cannot create new log
	// lines, and truncation cannot expose a prefix of a redacted credential.
	var bounded strings.Builder
	for _, r := range output {
		quoted := strconv.Quote(string(r))
		quoted = quoted[1 : len(quoted)-1]
		if bounded.Len()+len(quoted) > claudeDiagnosticLimit {
			return `"` + bounded.String() + `" [truncated]`
		}
		bounded.WriteString(quoted)
	}
	return `"` + bounded.String() + `"`
}
