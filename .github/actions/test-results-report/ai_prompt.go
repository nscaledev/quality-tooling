package main

import "fmt"

func claudePrompt() string {
	return fmt.Sprintf(`Analyze these test failures and skips.

The GitHub step summary already includes run totals, links, environment details, actor information, and previous-result comparisons. Do not repeat that information.

Evidence priority:

1. Suite report evidence (JUnit/XML/JSON): status, failure message, output, suite, timestamps, resource identifiers.
2. Matching Kubernetes/Unikorn CR evidence for the same resource and time window.
3. Matching Grafana observations for the same resource and time window.
4. Other correlated environmental observations.

Prefer higher-priority evidence whenever it sufficiently explains a failure. Do not override suite-report evidence with lower-priority observations.

Confidence guidance:

- High confidence: suite report evidence plus matching CR or Grafana evidence.
- Medium confidence: suite report evidence alone clearly explains the failure.
- Low confidence: incomplete, conflicting, missing, or weakly correlated evidence.
- Reflect confidence through wording but do not add a confidence column.

Output exactly two sections separated by a line containing only:

%s

Do not output this delimiter anywhere else.

==================================================
SECTION 1: GitHub Step Summary (Markdown)
==================================================

Start with:

## Test Failure Analysis

Keep the report concise.
Keep the report close to the existing production format.

Include:

### Patterns

| Category | What failed | Why it failed | Likely reason | Impact | Next check |
| --- | --- | --- | --- | ---: | --- |

Classification must be one of:

- infra/external
- configuration
- code/core logic
- test/false failure
- skipped
- unknown/mixed

Classification rules:

- skipped
  - All affected tests are skipped.
  - Includes known-bug skips, disabled tests, pending tests, intentional skips, and sentinel skips.

- test/false failure
  - Test code bugs.
  - Invalid assertions.
  - Sentinel failures.
  - Harness issues.
  - False failures.
  - Other failures caused by test logic rather than product behavior.

- code/core logic
  - Product behavior contradicts expected behavior.
  - Functional regressions.
  - Core workflow failures caused by product logic.

- configuration
  - Credentials.
  - Permissions.
  - Feature flags.
  - Environment configuration.
  - Provisioning configuration.
  - Quota configuration.
  - Misconfiguration before product behavior is exercised.

- infra/external
  - Infrastructure.
  - Platform failures.
  - Network failures.
  - External service dependency failures.
  - Capacity issues.
  - Availability issues.
  - Provisioning failures caused by underlying systems.

- unknown/mixed
  - Evidence is insufficient.
  - Evidence conflicts.
  - Root cause cannot be confidently determined.

Pattern requirements:

- Group failures and skips by likely area or pattern.
- Do not group by individual test.
- Order patterns by operational significance before count.
- A single infrastructure blocker may be more important than many skipped tests.
- Use representative tests only when they help explain the pattern.
- Limit representative examples to at most two test names per pattern.
- Include affected counts only for that pattern.
- Do not restate overall run totals.

Failure interpretation requirements:

- Base analysis on suite-report evidence first.
- Use failed/skipped status, suite, messages, output, timestamps, and resource identifiers.
- When file/line locations point to test files in the checked-out repository, inspect nearby test code and fixtures for dependency context, such as setup resources, helper-created networks, parent resources, or lifecycle prerequisites.
- Use test code context only to understand relationships between resources; do not invent resource IDs, backend components, or root causes from source code alone.
- Do not quote source code or add raw source snippets to the report.
- Use Grafana observations and CR observations only as supporting evidence inside existing pattern rows or next-check bullets.
- Combine suite evidence, Grafana observations, and CR observations into a single interpretation.
- Use test history observations only as recurrence context. A previous failed test-history record is not proof of the current root cause unless current suite, Grafana, or CR evidence supports the same reason.
- When test history shows the same test or failure fingerprint failed before, mention it only if it changes the likelihood or next check for the current failure.
- Keep environment, region, resource ID, and evidence signals scoped to the matching failure and time window.
- When grouped failures have different concrete Grafana or CR signals, split the row or explicitly qualify each signal by environment/resource.
- Do not carry VLAN IDs, physical networks, controller errors, or CR states from one failure, resource, region, or environment to another.
- Do not add a separate Grafana section.
- Do not add a separate Kubernetes section.
- Do not produce separate Grafana sections.
- Do not produce separate Kubernetes sections.
- Do not produce source-by-source analysis.

Grafana evidence rules:

- Use Grafana observations only when directly relevant and concrete.
- Mention Grafana only when it supports the failure interpretation or changes the next action.
- When Grafana signal includes a concrete controller error, put that exact signal in the Likely reason or Next check instead of generic wording like "controller issue" or "provisioning problem".
- For provisioning timeouts, connect dependency logs to the blocked resource when the evidence shows a dependency relationship, for example a load balancer stuck because its network dependency failed.
- Examples:
  - Grafana showed INTERNAL_ERROR.
  - Grafana showed connection refused.
  - Grafana showed vlan ids exhausted.
  - Grafana showed VlanIdInUse: VLAN 1101 on physical network physnet1 is in use.
- Mention a specific VLAN ID or physical network only when the matched evidence for that exact resource and time window contains it.
- Treat "allocation failure: vlan ids exhausted" as allocation-pool exhaustion, not proof that a specific VLAN ID is in use.
- Do not overstate certainty when Grafana returned empty, cleanup-only, or loosely related logs.
- Do not overstate certainty when Grafana returned empty results, cleanup-only logs, or loosely related activity.
- Do not promote weak, time-disjoint, or identifier-unmatched Grafana observations into root cause.
- If a failed test time range and Grafana query time range are both present, compare them before making timing claims.
- Do not claim an error occurred before the Grafana capture window unless the failed test began before that window.
- If the failed test is inside the Grafana window but Grafana only returned cleanup, audit, or activity rows, state that the provisioning/error signal was not present in the returned Grafana lines only when those rows directly match the failed resource and change the next action.
- Point the next check toward the resource creation or provisioning transition period within the test window when cleanup/audit/activity rows directly match the failed resource.
- If Grafana evidence is not actionable, omit it rather than explaining why it is weak.

CR evidence rules:

- Use CR observations only when directly relevant and concrete.
- Mention CR observations only when they support the failure interpretation or change the next action.
- Examples:
  - Network CR status phase=Error reason=VLANExhausted.
  - CR lookup found no matching Network.
  - CR query failed with forbidden.
- Do not overstate certainty when CR lookup returned no objects, CR fields are missing, matches are weak, query results are incomplete, or queries failed.
- If CR evidence is weak or non-actionable, omit it.

The pattern table must clearly communicate:

- What failed.
- Why it failed.
- Likely reason.
- Impact.
- Next check.

Optional:

### Representative Failed Tests

Include only when test-level detail materially helps explain a pattern. Maximum 10 rows. Group duplicate failure reasons into a single row.

Format:

| Suite / area | Representative tests | Failure reason | Count |
| --- | --- | --- | ---: |

### Suggested Next Checks

Provide 2-4 actionable bullets.

Examples:

- Confirm whether failures share the same status or error before opening individual issues.
- Validate credentials before rerunning representative suites.
- Inspect provisioning transitions for affected resources.
- Verify infrastructure capacity before rerunning.

Do not include:

- Separate Failed Tests sections.
- Separate Skipped Tests sections.
- Raw logs.
- Raw CR YAML.
- Raw CR JSON.
- Grafana URLs.
- LogQL.
- Search terms.
- kubectl commands.
- Resource dumps.
- Full lists of tests.

Use this shape:
## Test Failure Analysis

### Patterns
| Category | What failed | Why it failed | Likely reason | Impact | Next check |
| --- | --- | --- | --- | ---: | --- |
| configuration | Auth-dependent setup across suites | API calls returned 401 before product assertions | Expired or invalid API token | 23 failed, 37 skipped | Validate the API token, then rerun one representative suite |

### Representative Failed Tests
| Suite / area | Representative tests | Failure reason | Count |
| --- | --- | --- | ---: |
| File Storage Management | attach storage, detach storage | HTTP 401 access_denied before product assertions | 8 |

### Suggested Next Checks
- Confirm whether the failures share the same status/error before opening individual test issues.
- Validate credentials before rerunning representative suites.
- Inspect provisioning transitions for affected resources.

==================================================
SECTION 2: Slack Summary (plain text)
==================================================

Output 4-6 high-signal Slack mrkdwn bullet lines. Do not use tables.
Keep Slack as an overall summary by suite/failure category.

Pattern bullet format:

- *<suite/category>* (<category>): ...

Where category is one of:

- infra/external
- configuration
- code/core logic
- test/false failure
- skipped
- unknown/mixed

Each pattern bullet must explain:

- Which suite or area failed.
- What failed.
- Why it failed.
- Likely reason.

Slack evidence rules:

Grafana-backed bullets:

- Include Grafana only when it directly supports the failure interpretation or changes the next action.
- Explicitly connect the test error, interpretation, and Grafana signal in the same bullet.

CR-backed bullets:

- Include CR observations only when they directly support the failure interpretation or change the next action.
- Explicitly connect the test error, interpretation, and CR signal in the same bullet.

Do not:

- Do not use vague phrases like "Grafana returned related activity".
- Do not use vague phrases like "CR state looked related".
- Do not mention Grafana merely to say correlation was weak.
- Do not mention Grafana merely to say evidence was time-disjoint.
- Do not mention Grafana merely to say evidence was identifier-unmatched.
- Do not mention Grafana merely to say evidence was probably unrelated.
- Do not say "before the captured window" when the failed test start/end times are inside the Grafana query window.
- Do not mention CR merely to say it looked related.
- Do not mention vague CR state observations.
- Do not mention weak CR matches without a concrete signal.

Cleanup-only Grafana handling:

- Mention cleanup/audit/activity rows only when they directly match the failed resource and change the next action.
- If Grafana returned relevant errors, name the signals, such as INTERNAL_ERROR, connection refused, or vlan ids exhausted.

Grouping rules:

- Group by suite when one suite is affected.
- Group by shared root cause when multiple suites share the same cause.

Ordering rules:

- Lead with the highest-attention product, infra, configuration, or environment blocker.
- Do not prioritize by count alone.
- Keep temporary sentinel/test-validation failures short unless they are the only issue.
- Include only the evidence needed to justify the category.

Skip handling:

- For intentional skips, known-bug skips, disabled tests, pending tests, and sentinel skips, use category "skipped" and briefly state when the skip should be removed or re-enabled.

Sentinel failure handling:

- For intentional sentinel failures, use category "test/false failure" and briefly state that the failure is temporary and should be removed or disabled before review.

Do not:

- Do not list every failed test.
- Do not list every skipped test.
- Do not list every failed or skipped test.
- Do not restate the test run title, environment, branch, actor, or full totals line.
- Do not restate run totals.
- Do not restate run title.
- Do not restate environment.
- Do not restate branch.
- Do not restate actor.
- Do not include selector names, file paths, or retry details unless they materially change the next action.
- Do not add Evidence bullets.
- Do not add Impact bullets.
- Do not add Details bullets.
- Do not add Confidence bullets.
- Do not mention issue alerting unless it appears in the evidence.

Finish with exactly one action bullet:

- *Action:* Use the GitHub build summary for detailed test-level failure reasons, then perform the highest-priority validation or focused rerun identified above.
- When failed tests are present, the action bullet must mention that detailed test-level failure reasons are available in the GitHub build summary.

If the run contains only skipped tests and no failures:

- *Action:* Mention only the highest-priority skip review or re-enable action.
- Do not mention test-level failure reasons.
- Do not mention test-level failure reasons for skip-only runs.

Use this shape:
- *Auth / all suites* (configuration): 23 setup-dependent tests failed with HTTP 401 before product assertions; the likely reason is an expired or invalid API token.
- *File Storage input validation* (skipped): 1 test is intentionally skipped for known bug INST-457; re-enable it once the bug is fixed.
- *File Storage attachment network* (infra/external): The test failed because network provisioning reached error instead of provisioned; Grafana showed vlan ids exhausted for the same resource during the test window, so inspect network capacity before rerunning.
- *Action:* Use the GitHub build summary for detailed test-level failure reasons, then validate credentials and rerun one focused smoke suite.`, aiSlackDelimiter)
}

func grafanaLogQueryPlanningPrompt() string {
	return `You are planning read-only Loki log queries for Grafana MCP based on parsed test failures.

Return strict JSON only. Do not include markdown, prose, explanations, comments, or code fences.

Output format:

{
  "queries": [
    {
      "failure_ref": "f1",
      "test_name": "uploads file",
      "backend_area": "file-storage",
      "expected_error": "POST /api/storage returned 500 for claim-123",
      "search_terms": ["claim-123","file-storage","500"],
      "logql": "{namespace=~\".+\"} |~ \"(?i)(claim-123|file-storage|500)\"",
      "reason": "The failed upload crossed the file storage API and includes a backend 500 response, so Loki evidence can confirm whether the service emitted the same error.",
      "confidence": "medium"
    }
  ]
}

If no backend-related log lookup is justified, return:

{"queries":[]}

Use the parsed failure evidence to determine whether backend log correlation is warranted.

Evaluate failures using the following evidence priority:

1. Failure message
2. Captured output
3. Resource identifiers
4. Environment-specific failure details
5. Previous-result comparison

Prefer higher-priority evidence whenever it sufficiently explains the failure.

Do not infer backend involvement from suite names, product areas, test locations, or filenames alone.

Create queries only when backend evidence would materially help confirm or explain the failure.

Backend evidence includes:

- API 4xx or 5xx responses
- Backend exception signatures
- Request IDs
- Trace IDs
- Correlation IDs
- Resource UUIDs
- Resource names
- Provisioning failures
- provisioningStatus mismatches
- state mismatches
- Infrastructure error states
- Cloud resource identifiers
- Dependent resource identifiers from API output or status fields, such as status.networkId for a load balancer
- Explicit service or component failures
- Timeout failures involving provisioning, orchestration, or backend APIs

Do not create queries for:

- Pure UI assertion failures
- Visual validation failures
- Client-side rendering failures
- Test-code failures
- Assertion mismatches with no backend signal
- Known false failures where backend evidence would not affect diagnosis

Use the exact failure_ref values from the input.

test_name must exactly match the corresponding Test value from the input.

expected_error must contain:

- the exact backend-facing error message when available, or
- the shortest exact backend error signature from the failure evidence

If no exact backend-looking error exists, use an empty string.

backend_area should identify the most likely backend component only when supported by the evidence.

Examples:

- file-storage
- networking
- identity
- provisioning
- compute
- billing
- cluster-lifecycle

If the evidence does not support a component assignment, use:

"unknown"

search_terms must contain only values copied from the failure evidence.

Allowed values:

- trace IDs
- request IDs
- correlation IDs
- UUIDs
- resource names
- API paths
- API error strings
- status codes
- backend component names
- provisioning states

Do not invent search terms.

Do not normalize, paraphrase, or expand identifiers.

Prefer identifiers in the following order:

1. Trace IDs
2. Request IDs
3. Correlation IDs
4. UUIDs
5. Resource names
6. API error strings
7. Status codes
8. Component names

Use the strongest identifiers available.

Dependency-aware lookup rules:

- For provisioning failures, include dependency IDs copied from captured output when they are part of the failed resource state.
- For load balancer provisioning timeouts, prefer a query that can see both the load balancer UUID and any status.networkId or POST /networks UUID from the same failure evidence.
- If the failed resource is waiting on a dependency, the useful backend error may be in the dependency controller logs, not only in the failed resource controller logs.
- If local test code is available, inspect the referenced test and fixture helpers to understand dependency setup, for example whether a load balancer test creates a network first.
- Do not invent dependency IDs or component names; use only dependency identifiers present in the failure evidence or failure keyword regex.

reason must be a single sentence explaining why backend log evidence is or is not needed for this failure.

Keep the reason grounded in the failure evidence.

Do not speculate about root causes.

confidence must be one of:

- high
- medium
- low

Use:

high:
- explicit backend error
- request ID
- trace ID
- UUID
- provisioning failure
- API 4xx/5xx
- direct backend component failure

medium:
- backend involvement is strongly implied but identifiers are limited

low:
- indirect backend indicators only

Generate one query per distinct backend failure signature.

Avoid duplicate queries that differ only by test name while searching for the same backend condition.

When multiple failures share the same backend error pattern, use the strongest representative failure evidence.

LogQL requirements:

- Read-only queries only.
- Keep queries concise and readable.
- Prefer the smallest query that still captures the strongest identifiers.
- Avoid broad regex searches when a unique identifier is available.
- Use broad selectors such as:

{namespace=~".+"}

for cross-component failures unless the evidence clearly supports a narrower scope.

Only narrow namespace or service selection when the failure evidence directly supports it.

Do not invent namespaces, services, labels, resources, request IDs, trace IDs, UUIDs, component names, status codes, API paths, or error strings.

Do not request writes or mutations.

Rules:

- Do not include Grafana URLs, Explore URLs, dashboard URLs, time ranges, explanatory text outside JSON, markdown, or comments.
- The reporter generates Grafana URLs separately using the datasource, query, and time window.`
}

func unikornCRQueryPlanningPrompt() string {
	return `You are planning read-only Kubernetes custom-resource lookups for Unikorn test failure analysis.

Return strict JSON only. Do not include markdown, prose, explanations, comments, code fences, shell scripts, kubectl commands, or shell syntax.

Expected output:

{"queries":[{"failure_ref":"f1","test_name":"creates network","backend_area":"network","resource":"networks.region.unikorn-cloud.org","namespace":"default","name":"network-123","selector":"","all_namespaces":false,"reason":"The test failed because a VPC/network resource reached Error and the failure includes the Network CR name, so the CR status can confirm the backend state.","confidence":"high"}]}

If no Kubernetes CR lookup is justified, return:

{"queries":[]}

Use parsed failure evidence to decide whether a Kubernetes custom-resource lookup is warranted.

Evidence priority:

1. Failure message
2. Captured output
3. Resource identifiers
4. Environment-specific failure details
5. Previous-result comparison

The suite report is primary. Do not use CRs as the default failure source. Grafana/logs and Claude analysis should handle non-resource failures.

Create CR lookups only when Kubernetes custom-resource state could materially improve the failure analysis and the failure evidence contains a Unikorn/Kubernetes resource lifecycle signal.

Unikorn/Kubernetes resource lifecycle signals include:

- Provisioning timeouts
- provisioningStatus mismatches
- Error CR states
- Failed CR states
- Deleted or missing Kubernetes-owned resources
- Finalizers
- Owner references
- Controller-owned custom resources
- Cloud resource UUIDs
- VPC resource names
- Network resource names
- Instance resource names
- File storage resource names
- Kubernetes cluster resource names

For load balancer provisioning failures, do not request loadbalancers.region.unikorn-cloud.org. If the failure evidence includes dependency IDs such as status.networkId or POST /networks UUIDs, CR lookups for dependency resources such as networks.region.unikorn-cloud.org or vlanallocations.region.unikorn-cloud.org are allowed when they materially improve the analysis.

Do not create CR lookups for:

- Purely client-side assertions
- Local test framework failures
- Auth/token failures with no resource name
- API validation errors
- HTTP auth/config errors
- Generic 4xx/5xx responses without resource lifecycle evidence
- Cleanup-only not_found errors unless the failure is about a Kubernetes-owned resource lifecycle
- Direct load balancer CR lookups; use Grafana/API evidence for the load balancer itself because the CR reader path does not currently expose loadbalancers.region.unikorn-cloud.org
- Non-resource failures that are better handled by Grafana/logs or summary analysis

Do not infer Kubernetes CR involvement from suite names, product areas, test locations, or filenames alone.

Use the exact failure_ref values from the input.

test_name must exactly match the input Test value for that failure_ref.

backend_area should name the likely backend component or area only when supported by the failure evidence.

Examples:

- network
- vlan
- load-balancer
- server
- file-storage
- compute
- object-storage
- identity
- kubernetes-cluster
- provisioning

If the evidence does not support a component assignment, use:

"unknown"

resource must be exactly one kubectl resource identifier.

Prefer plural.group form.

Examples supported by the github-unikorn-cr-reader bot include:

- networks.region.unikorn-cloud.org
- vlanallocations.region.unikorn-cloud.org
- servers.region.unikorn-cloud.org
- filestorages.region.unikorn-cloud.org
- computeinstances.compute.unikorn-cloud.org
- objectstorageendpoints.storage.unikorn-cloud.org
- projects.identity.unikorn-cloud.org
- kubernetesclusters.unikorn-cloud.org

resource must never include:

- spaces
- flags
- pipes
- shell syntax
- kubectl commands
- multiple resources

Include either name or selector.

Prefer lookup keys in this order:

1. Exact CR name from the failure evidence
2. Resource UUID from the failure evidence
3. Cloud resource name from the failure evidence
4. Controller-owned resource name from the failure evidence
5. Label selector copied from the failure evidence

Prefer exact name over selector whenever available.

Do not invent names, UUIDs, namespaces, selectors, resource kinds, groups, labels, or backend areas.

namespace rules:

- Set namespace when the evidence provides it.
- If namespace is unknown for a namespaced CR, set all_namespaces to true.
- If namespace is known, set all_namespaces to false.
- For cluster-scoped CRs, use an empty namespace and set all_namespaces to false.
- Do not invent a default namespace unless the evidence explicitly says default.

name and selector rules:

- If using name, selector must be an empty string.
- If using selector, name must be an empty string.
- Prefer exact names whenever possible.
- Use selector only when a label selector or uniquely useful selector is present in the failure evidence.
- Do not synthesize selectors from vague test names or product areas.

reason must be one consolidated sentence explaining why this specific failure needs Kubernetes CR state.

The reason must connect:

- the test failure,
- the resource lifecycle signal,
- and why CR state can improve the analysis.

confidence must be one of:

- high
- medium
- low

Use:

high:
- exact CR name is present
- exact resource name is present
- exact UUID is present
- explicit provisioning timeout is present
- explicit Error or Failed CR state is present
- missing/deleted Kubernetes-owned resource is the failure

medium:
- Kubernetes-owned lifecycle is strongly implied, but the exact CR name or namespace is incomplete

low:
- resource lifecycle evidence is indirect, incomplete, or weak, but a CR lookup may still help

Generate one query per distinct Kubernetes-owned resource or distinct CR failure signature.

Avoid duplicate queries that differ only by test name while looking up the same resource or same lifecycle condition.

When multiple failures share the same resource or same CR lifecycle signature, use the strongest representative failure evidence.

The collector is read-only and only runs kubectl get/list on requested CRs.

Do not request:

- pods
- logs
- exec
- secrets
- configmaps
- events
- loadbalancers.region.unikorn-cloud.org
- writes
- deletes
- patches
- mutations

Do not include:

- kubectl commands
- shell scripts
- shell syntax
- flags
- pipes
- raw YAML requests
- raw JSON requests
- explanatory text outside JSON
- markdown
- comments`
}
