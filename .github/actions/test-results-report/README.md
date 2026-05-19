# Test Results Report Action

Analyze test failures and skips, write a GitHub Actions step summary, optionally compare against previous results, optionally run Claude failure analysis, and optionally notify Slack.

This action is additive. Existing users of `slack-test-notifications` can keep using it unchanged.

## Features

- Supports `ginkgo-json`, `junit`, and `playwright-json`
- Defaults to `format: auto`
- Writes a GitHub step summary by default
- Compares against previous results when `previous-results-path` is provided
- Reports new, recurring, and resolved failures/skips
- Sends Slack via incoming webhook or Slack bot `chat.postMessage`
- Optionally adds Claude failure analysis
- Fails open for Slack and Claude by default

## Basic Usage

```yaml
- name: Report test results
  uses: nscaledev/quality-tooling/.github/actions/test-results-report@main
  if: ${{ !cancelled() }}
  with:
    test-results-path: packages/e2e-console/test-results/results.xml
    format: junit
    title: E2E Test Results
    environment: dev
```

## Console E2E Style Usage

Place this after the Allure report URL is known.

```yaml
- name: Report E2E results
  uses: nscaledev/quality-tooling/.github/actions/test-results-report@main
  if: always() && (github.event_name == 'schedule' || github.ref == 'refs/heads/main')
  with:
    test-results-path: artifacts/test-results/results.xml
    format: junit
    title: E2E Test Results
    environment: ${{ needs.e2e-smoke-tests.outputs.target-env }}
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    report-url: ${{ steps.report-url.outputs.url }}
    slack-bot-token: ${{ secrets.E2E_UI_AUTOMATION_SLACK_BOT_TOKEN }}
    slack-channel: ${{ vars.E2E_SLACK_CHANNEL_NAME }}
    enable-ai-analysis: 'true'
    claude-token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}
```

## Previous Result Comparison

For MVP, previous results are read from a local path. The path can be a file or a directory. Directory mode recursively picks the newest supported result file named `results.xml`, `junit.xml`, `results.json`, or `test-results.json`.

```yaml
with:
  test-results-path: artifacts/test-results/results.xml
  previous-results-path: previous-artifacts/test-results/results.xml
  compare-with-previous: auto
```

When enabled, the report includes:

- new failures
- recurring failures
- resolved failures
- new skips
- recurring skips
- resolved skips
- pass/fail/skip deltas
- duration delta

## Inputs

| Input | Required | Default | Description |
| --- | --- | --- | --- |
| `test-results-path` | Yes | - | Current results file or directory |
| `format` | No | `auto` | `auto`, `ginkgo-json`, `junit`, `playwright-json` |
| `previous-results-path` | No | empty | Previous results file or directory |
| `previous-results-format` | No | `auto` | Format for previous results |
| `previous-results-source` | No | `path` | Only `path` is currently supported |
| `compare-with-previous` | No | `auto` | Auto enables comparison when previous path is set |
| `write-step-summary` | No | `true` | Append markdown to `$GITHUB_STEP_SUMMARY` |
| `send-slack` | No | `auto` | Auto sends when Slack credentials are supplied |
| `slack-webhook-url` | No | empty | Incoming webhook URL |
| `slack-bot-token` | No | empty | Bot token for `chat.postMessage` |
| `slack-channel` | No | empty | Slack channel for bot mode |
| `fail-on-slack-error` | No | `false` | Fail action on Slack errors |
| `environment` | No | empty | Environment label |
| `branch` | No | `GITHUB_REF_NAME` | Branch shown in Slack |
| `actor` | No | `GITHUB_ACTOR` | Actor shown in Slack |
| `title` | No | `Test Results` | Report title |
| `workflow-url` | No | inferred | GitHub Actions workflow URL |
| `report-url` | No | empty | Published report URL, e.g. Allure |
| `max-failures` | No | `5` | Failure detail limit |
| `max-skips` | No | `10` | Skip detail limit |
| `include-skips` | No | `true` | Include skipped test details in summary |
| `enable-ai-analysis` | No | `false` | Run Claude analysis |
| `claude-token` | No | empty | Claude Code OAuth token |

## Outputs

The action emits counts and comparison values:

- `total`
- `passed`
- `failed`
- `skipped`
- `duration`
- `duration-ms`
- `conclusion`
- `new-failures`
- `recurring-failures`
- `resolved-failures`
- `new-skips`
- `recurring-skips`
- `resolved-skips`
- `slack-sent`

## Backward Compatibility

This action does not replace or change `slack-test-notifications`. Existing Ginkgo webhook consumers can continue using that action.

For new migrations, use this action. It preserves the old webhook model through `slack-webhook-url` while also supporting bot-token Slack messages and non-Ginkgo result formats.
