# Slack Test Notifications Action

Send a Slack message summarising Ginkgo/Gomega test results from nightly CI runs.

## Features

- Posts pass/fail/skip counts, total tests, and test duration
- Lists up to 5 individual failures with error messages and file locations
- Configurable title per repository (e.g. "Compute API Test Results")
- Runs even when tests fail via `if: ${{ !cancelled() }}`
- No external dependencies — pure Go standard library

## Test Results Format

This action reads **Ginkgo v2 JSON test results**. Generate them in your tests with:

```bash
ginkgo -r --json-report=test-results.json ./test/...
```

The file must be a JSON array of suite reports matching the Ginkgo v2 output structure. The action processes the first suite in the array.

## Requirements

### Slack Setup

1. Create an **Incoming Webhook** in your Slack workspace:
   - Go to your Slack app settings > Incoming Webhooks
   - Create a new webhook for the target channel
   - Copy the webhook URL (starts with `https://hooks.slack.com/services/...`)

2. Add the webhook URL to your repository:
   - Go to Settings > Secrets and variables > Actions > Secrets
   - Add secret: `SLACK_WEBHOOK_URL`

## Usage

### Basic Example

```yaml
- name: Send Slack Notification
  uses: nscaledev/quality-tooling/.github/actions/slack-test-notifications@main
  if: ${{ !cancelled() }}
  with:
    test-results-path: test/api/suites/test-results.json
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    slack-webhook-url: ${{ secrets.SLACK_WEBHOOK_URL }}
    environment: dev
```

### Complete Workflow Example

```yaml
name: API Tests
on:
  schedule:
    - cron: '0 6 * * *'
  workflow_dispatch:

jobs:
  api-tests-dev:
    name: API Tests (dev)
    runs-on: ubuntu-latest
    env:
      ENVIRONMENT: dev

    steps:
    - name: Checkout
      uses: actions/checkout@v4

    - name: Setup Go
      uses: actions/setup-go@v3
      with:
        go-version-file: go.mod
        cache: true

    - name: Run API Tests
      run: make test-api

    - name: Archive Test Results
      uses: actions/upload-artifact@v4
      if: ${{ !cancelled() }}
      with:
        name: api-test-results-dev
        path: test/api/suites/test-results.json

    - name: Create Linear Issues for Test Failures
      uses: nscaledev/quality-tooling/.github/actions/linear-test-failures@main
      if: ${{ !cancelled() }}
      with:
        test-results-path: test/api/suites/test-results.json
        workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        linear-api-key: ${{ secrets.LINEAR_API_KEY }}
        linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
        linear-project-id: ${{ vars.LINEAR_PROJECT_ID }}
        environment: dev
        linear-priority: '3'
        max-failures: '5'

    - name: Send Slack Notification
      uses: nscaledev/quality-tooling/.github/actions/slack-test-notifications@main
      if: ${{ !cancelled() }}
      with:
        test-results-path: test/api/suites/test-results.json
        workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        slack-webhook-url: ${{ secrets.SLACK_WEBHOOK_URL }}
        environment: dev
        title: 'Compute API Test Results'
```

## Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `test-results-path` | Path to Ginkgo JSON test results file | Yes | - |
| `workflow-url` | GitHub Actions workflow run URL | Yes | - |
| `slack-webhook-url` | Slack incoming webhook URL | Yes | - |
| `environment` | Environment name (dev, uat, prod, etc.) | Yes | - |
| `title` | Header title shown in the Slack message | No | `API Test Results` |

## Slack Message Format

The notification contains:

- **Header**: `{title} ({ENVIRONMENT})` — e.g. `Compute API Test Results (DEV)`
- **Suite status**: Suite description with pass/fail indicator
- **Stats fields**: Total tests, duration, passed, failed, skipped, start time
- **Failure details** *(if any)*: Up to 5 failures, each showing:
  - Test name (full container hierarchy)
  - File and line number
  - Error message (truncated at 500 chars)
  - Captured output (truncated at 300 chars)
- **Link**: Direct link to the GitHub Actions workflow run

## Troubleshooting

### "SLACK_WEBHOOK_URL environment variable not set"

Add `SLACK_WEBHOOK_URL` to your repository secrets (Settings > Secrets and variables > Actions).

### Slack returns a non-200 response

- Verify the webhook URL is correct and the Slack app is still installed
- Check that the target channel still exists
- Regenerate the webhook if it has been revoked

### No test reports found

The test results file exists but contains an empty JSON array (`[]`). Ensure your test runner completed and wrote results before this step runs.
