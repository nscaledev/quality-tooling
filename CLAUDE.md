# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The `quality-tooling` repository provides company-wide, reusable GitHub Actions for quality assurance and CI/CD automation across all Nscale projects. Actions are centralized here to ensure consistency, maintainability, and easy version management across multiple repositories.

## Repository Structure

```
.github/actions/
  └── linear-test-failures/        # Auto-creates Linear issues for test failures
      ├── action.yml               # GitHub Composite Action definition
      ├── linear-issue-creator.go  # Go implementation (~718 lines)
      ├── go.mod                   # Go module definition
      ├── shared/
      │   └── types.go            # Ginkgo test result types
      └── README.md               # Action documentation
```

## Available Actions

### Linear Test Failures Action

**Purpose**: Automatically creates Linear issues for failed Ginkgo/Gomega tests from nightly CI runs.

**Key Features**:
- Creates one Linear issue per test failure with clear naming: `[Nightly-{env}] {TestSuite} - {TestName}`
- Duplicate detection using SHA256 hashing - adds comments to existing issues instead of creating duplicates
- Spam prevention - skips issue creation if failures exceed threshold (default: 5)
- Graceful error handling with retry logic - never fails CI workflows
- Supports multiple environments (dev, uat, prod)

**Usage in Other Repositories**:
```yaml
- uses: nscale/quality-tooling/.github/actions/linear-test-failures@v1
  with:
    test-results-path: path/to/test-results.json
    workflow-url: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
    linear-api-key: ${{ secrets.LINEAR_API_KEY }}
    linear-team-id: ${{ vars.LINEAR_TEAM_ID }}
    linear-project-id: ${{ vars.LINEAR_PROJECT_ID }}  # Optional
    environment: dev
```

## Development Commands

### Testing the Linear Action Locally

```bash
cd .github/actions/linear-test-failures

# Set environment variables
export LINEAR_API_KEY="lin_api_..."
export LINEAR_TEAM_ID="your-team-uuid"
export LINEAR_PRIORITY="3"
export MAX_FAILURES="5"

# Run with sample test results
go run linear-issue-creator.go \
  /path/to/test-results.json \
  "https://github.com/org/repo/actions/runs/123" \
  "dev"
```

### Running Go Tests

```bash
cd .github/actions/linear-test-failures
go test ./...
```

### Building Go Binary

```bash
cd .github/actions/linear-test-failures
go build -o bin/linear-issue-creator linear-issue-creator.go
```

## Architecture

### Core Components

1. **GitHub Composite Action** (`action.yml`): Defines inputs, environment variables, and execution steps. Runs the Go script with proper context.

2. **Go Implementation** (`linear-issue-creator.go`): Main logic for issue creation with key functions:
   - `main()` - Entry point with config loading
   - `processTestFailures()` - Main workflow orchestration
   - `searchExistingIssue()` - Queries Linear GraphQL API for duplicates
   - `createIssue()` - Creates new Linear issues
   - `addComment()` - Updates existing issues with new failures
   - `generateTestHash()` - Creates SHA256 hash for duplicate detection
   - `buildIssueTitle()` - Formats title as `[Nightly-{env}] {suite} - {test}`
   - `buildIssueDescription()` - Creates markdown with metadata
   - `executeGraphQLQuery()` - HTTP client with retry logic (3 retries, exponential backoff)
   - `getLabelIDs()` - Resolves label names to Linear IDs

3. **Shared Types** (`shared/types.go`): Ginkgo v2 test result structures:
   - `GinkgoReport` - Top-level test suite report
   - `SpecReport` - Individual test result
   - `SpecFailure` - Failure details with file location
   - `PreRunStats` - Test execution statistics

### Duplicate Detection Algorithm

Two-phase approach for efficient and accurate matching:

1. **Fast filtering**: Linear GraphQL search by labels (`automated-test`, `nightly-failure`, `environment:{env}`) and state (open, backlog, todo, in_progress)
2. **Precise matching**: Extract and compare SHA256 hash from HTML comment in issue description

Hash format: `SHA256({test_full_path}|{environment})`

This ensures same test in different environments creates separate issues, while repeated failures in same environment update the existing issue.

### Error Handling

- **Network errors**: 3 retries with exponential backoff (1s, 2s, 4s)
- **Rate limits (429)**: Respects `Retry-After` header
- **Server errors (5xx)**: 2 retries
- **Client errors (4xx)**: No retry, logs error and continues
- **Graceful degradation**: Issues logged but CI workflow never fails

## Versioning and Releases

### Creating New Releases

```bash
# Tag specific version
git tag -a v1.0.0 -m "Release v1.0.0

Features:
- Feature 1
- Feature 2"

# Update major version pointer (for v1.x.x)
git tag -f -a v1 -m "v1 major version"

# Push tags
git push origin v1.0.0
git push origin v1 --force  # Force needed to move v1 pointer
```

### Version Pinning Recommendations

Projects should pin to major versions for automatic minor/patch updates:
```yaml
uses: nscale/quality-tooling/.github/actions/{action}@v1  # Recommended
uses: nscale/quality-tooling/.github/actions/{action}@v1.0.0  # Specific version
```

### Semantic Versioning

- **Major (v2.0.0)**: Breaking changes to inputs, outputs, or behavior
- **Minor (v1.1.0)**: New features, backward-compatible changes
- **Patch (v1.0.1)**: Bug fixes, no new features

## Adding New Actions

1. Create directory structure:
```bash
mkdir -p .github/actions/{action-name}
cd .github/actions/{action-name}
```

2. Add `action.yml` with proper branding, inputs, and execution steps

3. Add implementation code (Go, Bash, etc.)

4. Add comprehensive `README.md` with:
   - Features and requirements
   - Usage examples (basic and complete workflow)
   - Input/output documentation
   - Troubleshooting section

5. Update root `README.md` with action description

6. Create version tags following semantic versioning

## Linear API Integration

### Required Linear Setup

**API Key**: Linear Settings > API > Create key (starts with `lin_api_`)

**Team ID**: Query via GraphQL explorer:
```graphql
{
  teams {
    nodes {
      id
      name
    }
  }
}
```

**Required Labels** (create in Linear team settings):
- `automated-test` (blue)
- `nightly-failure` (red)
- `environment:dev`, `environment:uat`, `environment:prod` (various colors)

### GraphQL Queries

The action uses Linear's GraphQL API endpoint: `https://api.linear.app/graphql`

Key queries:
- Search issues by labels and state
- Create issues with title, description, priority, and labels
- Add comments to existing issues
- Query label IDs by name

## Test Results Format

The action expects Ginkgo v2 JSON output format with structure:
- Top-level array of suite reports
- Each suite contains `SpecReports` array
- Each spec has `State` (passed/failed/skipped), `Failure` details, and location info

Generate test results in consuming projects:
```bash
ginkgo -r --json-report=test-results.json ./test/...
```

## Important Concepts

### Issue Naming Convention

Format: `[Nightly-{environment}] {TestSuite} - {TestName}`

Example: `[Nightly-dev] Core Cluster Management - should successfully create cluster`

Benefits:
- Prefix enables quick filtering by environment
- Test suite provides immediate context
- Human-readable test names match code
- Searchable and consistent across projects

### Spam Prevention

- Max failures threshold (default: 5) prevents issue flooding
- Logs message and skips creation when exceeded
- Configurable via `max-failures` input
- Duplicate detection prevents repeat issues
- 500ms delay between creations to avoid rate limits

### Manual vs. Automatic Closure

Issues are **never automatically closed** - engineers must manually close after verification. This design choice ensures:
- Human review of intermittent failures
- Pattern recognition across multiple runs
- Intentional issue triage and prioritization

When test passes again, duplicate detection adds comment to existing issue showing it's resolved in latest run, but issue remains open for human verification.

## Related Documentation

- `README.md` - Repository overview and quick start
- `TESTING.md` - Comprehensive testing guide with local and CI testing strategies
- `PLAN.md` - Implementation plan and architecture decisions
- `.github/actions/linear-test-failures/README.md` - Action-specific usage documentation

## License

Apache 2.0 - See LICENSE file for details.
