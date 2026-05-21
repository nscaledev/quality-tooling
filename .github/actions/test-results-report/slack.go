package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type SlackOptions struct {
	Title              string
	Environment        string
	Branch             string
	Actor              string
	WorkflowURL        string
	ReportURL          string
	AIAnalysis         string
	MaxFailures        int
	OmitFailureDetails bool
}

type SlackPayload struct {
	Text   string       `json:"text"`
	Blocks []SlackBlock `json:"blocks,omitempty"`
}

type SlackBlock struct {
	Type     string         `json:"type"`
	Text     *SlackText     `json:"text,omitempty"`
	Fields   []SlackText    `json:"fields,omitempty"`
	Elements []SlackElement `json:"elements,omitempty"`
}

type SlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type SlackElement struct {
	Type string     `json:"type"`
	Text *SlackText `json:"text,omitempty"`
	URL  string     `json:"url,omitempty"`
}

func buildSlackPayload(analysis Analysis, options SlackOptions) SlackPayload {
	if options.Title == "" {
		options.Title = "Test Results"
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = 10
	}
	hasAIAnalysis := strings.TrimSpace(options.AIAnalysis) != ""

	statusText := "Passed"
	statusEmoji := ":white_check_mark:"
	if analysis.Stats.Failed > 0 {
		statusText = "Failed"
		statusEmoji = ":x:"
	}

	envSuffix := ""
	if options.Environment != "" {
		envSuffix = fmt.Sprintf(" (%s)", strings.ToUpper(options.Environment))
	}

	text := fmt.Sprintf("%s%s %s - %s", options.Title, envSuffix, firstNonEmpty(analysis.Current.Name, "Test run"), statusText)
	blocks := []SlackBlock{
		{
			Type: "section",
			Text: &SlackText{Type: "mrkdwn", Text: fmt.Sprintf("%s *%s*", statusEmoji, text)},
		},
		{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*Total:*\n%d", analysis.Stats.Total)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Duration:*\n%s", formatDuration(analysis.Current.Duration))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Passed:*\n%d", analysis.Stats.Passed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Failed:*\n%d", analysis.Stats.Failed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Skipped:*\n%d", analysis.Stats.Skipped)},
			},
		},
	}

	var contextFields []SlackText
	if options.Environment != "" {
		contextFields = append(contextFields, SlackText{Type: "mrkdwn", Text: fmt.Sprintf("*Environment:*\n`%s`", options.Environment)})
	}
	if options.Branch != "" {
		contextFields = append(contextFields, SlackText{Type: "mrkdwn", Text: fmt.Sprintf("*Branch:*\n`%s`", options.Branch)})
	}
	if options.Actor != "" {
		contextFields = append(contextFields, SlackText{Type: "mrkdwn", Text: fmt.Sprintf("*Triggered by:*\n`%s`", options.Actor)})
	}
	if len(contextFields) > 0 {
		blocks = append(blocks, SlackBlock{Type: "section", Fields: contextFields})
	}

	if analysis.Compare != nil {
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*New failures:*\n%d", len(analysis.Compare.NewFailures))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Recurring failures:*\n%d", len(analysis.Compare.RecurringFailures))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Resolved failures:*\n%d", len(analysis.Compare.ResolvedFailures))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*New skips:*\n%d", len(analysis.Compare.NewSkips))},
			},
		})
	}

	if len(analysis.Failures) > 0 && !options.OmitFailureDetails {
		blocks = append(blocks, SlackBlock{Type: "divider"})
		heading := "*Failed Tests:*"
		formatFailure := formatSlackFailure
		if hasAIAnalysis {
			visible := min(len(analysis.Failures), options.MaxFailures)
			heading = fmt.Sprintf("*Failed Tests (first %d of %d):*", visible, len(analysis.Failures))
			formatFailure = formatSlackFailureCompact
		}
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{Type: "mrkdwn", Text: heading},
		})
		for i, failure := range analysis.Failures {
			if i >= options.MaxFailures {
				blocks = append(blocks, SlackBlock{
					Type: "section",
					Text: &SlackText{Type: "mrkdwn", Text: fmt.Sprintf("_...and %d more failures_", len(analysis.Failures)-options.MaxFailures)},
				})
				break
			}
			blocks = append(blocks, SlackBlock{
				Type: "section",
				Text: &SlackText{Type: "mrkdwn", Text: formatFailure(failure)},
			})
		}
	}

	if hasAIAnalysis {
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{Type: "mrkdwn", Text: fmt.Sprintf(":mag: *Failure Analysis*\n%s", truncate(strings.TrimSpace(options.AIAnalysis), 2400))},
		})
	}

	var actions []SlackElement
	if options.WorkflowURL != "" {
		actions = append(actions, SlackElement{
			Type: "button",
			Text: &SlackText{Type: "plain_text", Text: "GitHub Build"},
			URL:  options.WorkflowURL,
		})
	}
	if options.ReportURL != "" {
		actions = append(actions, SlackElement{
			Type: "button",
			Text: &SlackText{Type: "plain_text", Text: "Allure Report"},
			URL:  options.ReportURL,
		})
	}
	if len(actions) > 0 {
		blocks = append(blocks, SlackBlock{Type: "actions", Elements: actions})
	}

	return SlackPayload{
		Text:   text,
		Blocks: blocks,
	}
}

func sendSlack(ctx context.Context, config Config, payload SlackPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.SlackWebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send slack request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

func formatSlackFailure(test TestCase) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Test:* %s\n", test.Name))
	if test.Suite != "" {
		sb.WriteString(fmt.Sprintf("*Suite:* `%s`\n", test.Suite))
	}
	if location := formatSlackLocation(test); location != "" {
		sb.WriteString(fmt.Sprintf("*Location:* `%s`\n", location))
	}
	if test.Message != "" {
		sb.WriteString(fmt.Sprintf("*Error:*\n```\n%s\n```", truncate(cleanOneLine(test.Message), 500)))
	}
	return sb.String()
}

func formatSlackFailureCompact(test TestCase) string {
	var sb strings.Builder
	name := formatSlackFailureName(test)
	if test.Suite != "" {
		sb.WriteString(fmt.Sprintf("- *%s:* %s", test.Suite, name))
	} else {
		sb.WriteString(fmt.Sprintf("- *%s*", name))
	}
	if location := formatSlackLocation(test); location != "" {
		sb.WriteString(fmt.Sprintf(" (`%s`)", location))
	}
	if reason := firstNonEmpty(test.Message, test.RawState); reason != "" {
		sb.WriteString(fmt.Sprintf("\n  _Reason:_ %s", truncate(cleanOneLine(reason), 220)))
	} else if test.Output != "" {
		sb.WriteString("\n  _Reason:_ See GitHub build summary for captured output.")
	}
	return sb.String()
}

func formatSlackFailureName(test TestCase) string {
	name := firstNonEmpty(test.Name, test.ID, "Unnamed test")
	if test.Suite == "" {
		return name
	}
	for _, separator := range []string{" > ", " / ", ": "} {
		prefix := test.Suite + separator
		if strings.HasPrefix(name, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(name, prefix))
		}
	}
	return name
}

func formatSlackLocation(test TestCase) string {
	location := formatLocation(test)
	if location == "" {
		return ""
	}
	if test.File == "" {
		return location
	}
	location = filepath.Base(test.File)
	if test.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, test.Line)
	}
	return location
}
