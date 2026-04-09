# quality-tooling

Company-wide quality tooling, centralized for reusability across all projects.

## Available Actions

### Linear Test Failures

Automatically create Linear issues for failed Ginkgo/Gomega tests from nightly CI runs.

**Location**: `.github/actions/linear-test-failures/`

**Usage**:
```yaml
- uses: nscaledev/quality-tooling/.github/actions/linear-test-failures@main
  with:
    test-results-path: path/to/test-results.json
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    linear-api-key: ${{ secrets.LINEAR_API_KEY }}
    linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
    environment: dev
```

**Features**:
- Creates one issue per test failure
- Duplicate detection with comment updates
- Spam prevention (max 5 failures)
- Clear naming: `[Nightly-{env}] {TestSuite} - {TestName}`

[Full documentation](./.github/actions/linear-test-failures/README.md)

### Slack Test Notifications

Send a Slack message summarising Ginkgo/Gomega test results — pass/fail counts, duration, and details of up to 5 failures.

**Location**: `.github/actions/slack-test-notifications/`

**Usage**:
```yaml
- uses: nscaledev/quality-tooling/.github/actions/slack-test-notifications@main
  with:
    test-results-path: path/to/test-results.json
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    slack-webhook-url: ${{ secrets.SLACK_WEBHOOK_URL }}
    environment: dev
    title: 'Compute API Test Results'  # optional, defaults to "API Test Results"
```

**Features**:
- Posts pass/fail/skip counts and test duration
- Lists up to 5 failures with error messages and file locations
- Configurable title per repository
- Runs even when tests fail (`if: ${{ !cancelled() }}`)

[Full documentation](./.github/actions/slack-test-notifications/README.md)

## Adding New Actions

1. Create directory: `.github/actions/{action-name}/`
2. Add `action.yml` with action definition
3. Add `README.md` with usage documentation
4. Update this root README with action description
5. Commit and push to main branch
