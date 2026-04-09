# Linear Test Failures Action

Automatically create Linear issues for failed Ginkgo/Gomega tests from nightly CI runs.

## Features

- Creates one Linear issue per test failure for granular tracking
- Clear, searchable naming convention: `[Nightly-{env}] {TestSuite} - {TestName}`
- Duplicate detection - adds comments to existing issues instead of creating duplicates
- Spam prevention - skips issue creation if failures exceed threshold (default: 5)
- Rich metadata in issue descriptions (error messages, file locations, workflow links)
- Graceful error handling - never fails your CI workflow
- Supports multiple environments (dev, uat, prod, etc.)

## Test Results Format

This action analyzes **Ginkgo v2 JSON test results**. Your tests must generate a JSON file with this format:

**File**: `test/api/suites/test-results.json` (or any path you specify)

**Format**: Ginkgo v2 JSON output containing:
- Suite-level metadata (SuitePath, SuiteDescription, PreRunStats)
- Array of SpecReports with test results
- Each SpecReport includes:
  - `State`: "passed", "failed", "skipped", etc.
  - `ContainerHierarchyTexts`: Test suite hierarchy (e.g., ["Core Cluster Management", "When listing clusters"])
  - `LeafNodeText`: Actual test name (e.g., "should return all clusters")
  - `Failure`: Error details (Message, Location with FileName and LineNumber)
  - `CapturedGinkgoWriterOutput`: Test output/logs

**Generate this file in your tests**:
```bash
ginkgo -r --json-report=test-results.json ./test/...
```

**GitHub Actions Artifacts**:
When tests run, GitHub Actions will create artifacts like:
- `api-test-results-dev.zip` - Contains `test-results.json` (Ginkgo JSON format) ← **This is what the action uses**
- `api-test-junit-dev.zip` - Contains `junit.xml` (JUnit XML format, not used by this action)

The action reads the **JSON format** file (`test-results.json`), not the JUnit XML file.

**Example test result structure**:
```json
[
  {
    "SuitePath": "/path/to/tests",
    "SuiteDescription": "API Test Suites",
    "PreRunStats": { "TotalSpecs": 50, "SpecsThatWillRun": 50 },
    "SpecReports": [
      {
        "ContainerHierarchyTexts": ["Core Cluster Management", "When listing clusters"],
        "LeafNodeText": "should return all clusters for the organization",
        "State": "failed",
        "Failure": {
          "Message": "Expected status 200 but got 500: server error",
          "Location": { "FileName": "cluster_test.go", "LineNumber": 123 }
        },
        "CapturedGinkgoWriterOutput": "[DEBUG] API call logs here..."
      }
    ]
  }
]
```

## Requirements

### Linear Workspace Setup

1. **Create Linear API Key**:
   - Go to Linear Settings > API
   - Create new API key with name "GitHub Actions - Automated Tests"
   - Copy key (starts with `lin_api_`)

2. **Get Team ID**:
   - Use Linear GraphQL explorer: https://studio.apollographql.com/public/Linear-API/explorer
   - Query: `{ teams { nodes { id name } } }`
   - Copy the ID for your team

3. **Create Required Labels** (workspace-level or team-level):
   - `Automation Failures` - Marks issues created by automation
   - `nightly-failure` - Indicates test failure origin
   - `Dev` - Dev environment failures
   - `UAT` - UAT environment failures
   - `Prod` - Production environment failures (if applicable)
   - *(Labels should be capitalized as shown)*

4. **Get Project ID**:
   - If you want issues assigned to a specific Linear project
   - Use Linear GraphQL explorer: https://studio.apollographql.com/public/Linear-API/explorer
   - Query: `{ projects { nodes { id name } } }`
   - Copy the ID for your project (UUID format)

### GitHub Repository Setup

**Add these to your repository** (Settings > Secrets and variables > Actions):

1. **Secrets** (Secrets tab):
   - `LINEAR_API_KEY`: Your Linear API key (starts with `lin_api_`)

2. **Variables** (Variables tab):
   - `LINEAR_TEAM_ID`: Your Linear team ID in UUID format
     - Example: `4e120bab-0320-43e3-8047-7dbdf4b7f988` (Instances Squad)
   - `LINEAR_PRIORITY`: `3` (optional, 1=Urgent, 2=High, 3=Medium, 4=Low)
   - `LINEAR_PROJECT_ID`: Your Linear project ID (optional, UUID format)

**Example Setup**:
```
Repository: nscale/uni-compute
Secrets:
  LINEAR_API_KEY = lin_api_K0mEdRCqap...

Variables:
  LINEAR_TEAM_ID = 4e120bab-0320-43e3-8047-7dbdf4b7f988  # Instances Squad
  LINEAR_PRIORITY = 3
```

## Usage

### Basic Example

```yaml
- name: Create Linear Issues for Failures
  uses: nscale/quality-tooling/.github/actions/linear-test-failures@main
  if: ${{ !cancelled() }}  # Run even if tests fail
  with:
    test-results-path: test/api/suites/test-results.json
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    linear-api-key: ${{ secrets.LINEAR_API_KEY }}
    linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
    linear-project-id: ${{ vars.LINEAR_PROJECT_ID }}  # Optional
    environment: dev
```

### Complete Workflow Example

```yaml
name: API Tests
on:
  schedule:
    - cron: '0 6 * * *'  # Run nightly at 6am UTC
  workflow_dispatch:
    inputs:
      run_dev:
        description: 'Run Dev tests'
        type: boolean
        default: true
      run_uat:
        description: 'Run UAT tests'
        type: boolean
        default: false

permissions:
  contents: read

jobs:
  API-Tests-Dev:
    name: API Tests (dev)
    runs-on: ubuntu-latest
    if: github.event_name == 'schedule' || github.event.inputs.run_dev == 'true'
    env:
      ENVIRONMENT: dev
      API_BASE_URL: ${{ vars.DEV_API_BASE_URL }}

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

    - name: Archive API Test Results
      uses: actions/upload-artifact@v4
      if: ${{ !cancelled() }}
      with:
        name: api-test-results-dev
        path: test/api/suites/test-results.json

    - name: Archive API Test JUnit Report
      uses: actions/upload-artifact@v4
      if: ${{ !cancelled() }}
      with:
        name: api-test-junit-dev
        path: test/api/suites/junit.xml

    - name: Create Linear Issues for Test Failures
      uses: nscaledev/quality-tooling/.github/actions/linear-test-failures@main
      if: ${{ !cancelled() }}
      with:
        test-results-path: test/api/suites/test-results.json
        workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        linear-api-key: ${{ secrets.LINEAR_API_KEY }}
        linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
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

  API-Tests-UAT:
    name: API Tests (uat)
    runs-on: ubuntu-latest
    if: github.event_name == 'schedule' || github.event.inputs.run_uat == 'true'
    env:
      ENVIRONMENT: uat

    steps:
    # ... same steps as dev job ...

    - name: Create Linear Issues for Test Failures
      uses: nscaledev/quality-tooling/.github/actions/linear-test-failures@main
      if: ${{ !cancelled() }}
      with:
        test-results-path: test/api/suites/test-results.json
        workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        linear-api-key: ${{ secrets.LINEAR_API_KEY }}
        linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
        environment: uat
        linear-priority: '3'
        max-failures: '5'
```

**Key Integration Points**:

1. **Step Placement**: Add the Linear action **after** archiving test results but **before** notifications
   ```yaml
   - name: Run API Tests
   - name: Archive Test Results      # 1. Archive first
   - name: Create Linear Issues       # 2. Then create Linear issues
   - name: Send Slack Notification    # 3. Finally send notifications
   ```

2. **Use `if: ${{ !cancelled() }}`**: Ensures the action runs even when tests fail

3. **Environment Separation**:
   - Set `environment: dev` for dev job
   - Set `environment: uat` for UAT job
   - Same test failure in dev vs uat creates **separate** Linear issues

## Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `test-results-path` | Path to Ginkgo JSON test results file | Yes | - |
| `workflow-url` | GitHub Actions workflow run URL | Yes | - |
| `linear-api-key` | Linear API key for authentication | Yes | - |
| `linear-team-id` | Linear team ID (UUID) | Yes | - |
| `linear-project-id` | Linear project ID (UUID) to assign issues | No | - |
| `environment` | Environment name (dev, uat, prod) | Yes | - |
| `linear-priority` | Issue priority (1-4) | No | `3` (Medium) |
| `max-failures` | Max failures before skipping | No | `5` |

## What Happens When Tests Fail

### Real Example

Given this test result from a nightly run:
```json
{
  "SuitePath": "/home/runner/work/uni-compute/uni-compute/test/api/suites",
  "SuiteDescription": "API Test Suites",
  "PreRunStats": { "TotalSpecs": 50, "SpecsThatWillRun": 50 },
  "SpecReports": [
    {
      "ContainerHierarchyTexts": ["Core Cluster Management", "When listing compute clusters", "Given multiple clusters exist"],
      "LeafNodeText": "should return all clusters for the organization",
      "State": "failed",
      "Failure": {
        "Message": "Failed to create cluster: unexpected status code: expected 202, got 500, body: {\"error\":\"server_error\"}",
        "Location": {
          "FileName": "/home/runner/work/uni-compute/uni-compute/test/api/suites/cluster_management_test.go",
          "LineNumber": 145
        }
      },
      "CapturedGinkgoWriterOutput": "Checking cluster quota...\nCreating cluster...\nERROR: API returned 500"
    }
  ]
}
```

**The action will**:
1. Parse 50 test results: 49 passed, 1 failed
2. Check threshold: 1 <= 5 failures
3. Search Linear for existing issue with same test + environment
4. Create a new Linear issue (if not found):

**Created Issue**:
- **Identifier**: `INST-505` (Instances Squad)
- **Title**: `[Nightly-dev] Core Cluster Management - should return all clusters for the organization`
- **Labels**: Automation Failures, nightly-failure, Dev
- **Priority**: Medium (3)
- **Description**: Full error details, file location, captured output, workflow link
- **Metadata**: Hidden SHA256 hash for duplicate detection

**On subsequent failures** of the same test:
- Finds existing issue INST-505
- Adds comment: "Test failed again in run #79"
- Links to new workflow run
- Does NOT create duplicate issue

## How It Works

### Issue Creation

When tests fail:
1. Parses Ginkgo JSON test results file (e.g., `test/api/suites/test-results.json`)
2. Filters for failed tests (State == "failed")
3. Checks failure count threshold (default: ≤5)
4. For each failure:
   - Generates unique test hash (SHA256 of test path + environment)
   - Searches Linear for existing open issue with matching hash
   - If found: Adds comment with new failure details and workflow URL
   - If not found: Creates new issue with full details and metadata

### Issue Format

**Title**: `[Nightly-dev] Core Cluster Management - should successfully create cluster`

**Description includes**:
- Test suite and environment
- Test name and full path
- Error message with stack trace
- File location (filename and line number)
- Captured test output
- Workflow run link
- Hidden metadata for duplicate detection

### Duplicate Detection

Issues are deduplicated using:
1. **Fast filtering**: Linear API search by labels and state
2. **Exact matching**: SHA256 hash of test path + environment in metadata

This ensures:
- Same test failure in dev vs uat creates separate issues
- Same test failing multiple times updates the existing issue
- Issues remain accurate even if titles are manually edited

#### Issue States and Duplicate Detection

**Duplicate detection WILL find and update issues in these states:**
- **Backlog** - Not started
- **Todo** - Planned work
- **In Progress** - Currently being worked on
- **Blocked** - Waiting on dependencies
- Any other custom workflow states (except completed/canceled)

**Duplicate detection will NOT find issues in:**
- **Completed** - Issue marked as done/closed
- **Canceled** - Issue marked as won't fix/declined

### Spam Prevention

- **Max failures threshold**: Skips issue creation if >5 failures (configurable)
- **Duplicate detection**: Won't create multiple issues for same test
- **Rate limiting**: 500ms delay between issue creations
- **Retry logic**: Handles transient API errors gracefully

## Troubleshooting

### "LINEAR_API_KEY environment variable not set"

**Solution**: Add `LINEAR_API_KEY` to repository secrets.

### Issues not being created

**Causes**:
1. Failure count exceeds threshold (>5 by default) - check action logs for spam prevention message
2. Linear API error - check action logs for error messages

### Duplicate issues being created

**Causes**:
1. Metadata in issue description was removed/modified manually
2. Different environment name passed to action
3. Test name changed in codebase

**Solution**: Ensure issue descriptions aren't manually edited (especially HTML comments).

## Supported Test Frameworks

Currently supports:
- **Ginkgo v2** with Gomega matchers

Requirements:
- Tests must generate JSON output with `--json-report=test-results.json`
- JSON format must match Ginkgo v2 structure (see "Test Results Format" section)