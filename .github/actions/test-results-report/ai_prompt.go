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
- Use Grafana observations and CR observations only as supporting evidence inside existing pattern rows or next-check bullets.
- Combine suite evidence, Grafana observations, and CR observations into a single interpretation.
- Do not add a separate Grafana section.
- Do not add a separate Kubernetes section.
- Do not produce separate Grafana sections.
- Do not produce separate Kubernetes sections.
- Do not produce source-by-source analysis.

Grafana evidence rules:

- Use Grafana observations only when directly relevant and concrete.
- Mention Grafana only when it supports the failure interpretation or changes the next action.
- Examples:
  - Grafana showed INTERNAL_ERROR.
  - Grafana showed connection refused.
  - Grafana showed vlan ids exhausted.
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

Return strict JSON only. Do not include markdown, prose, or code fences.

Expected output:
{"queries":[{"failure_ref":"f1","test_name":"uploads file","backend_area":"file-storage","expected_error":"POST /api/storage returned 500 for claim-123","search_terms":["claim-123","file-storage","500"],"logql":"{namespace=~\".+\"} |~ \"(?i)(claim-123|file-storage|500)\"","reason":"The failed UI upload crossed the file storage API and includes a backend 500 signature, so Loki evidence can confirm whether file storage emitted the same error.","confidence":"medium"}]}

Rules:
- Inspect the failed test names, suites, locations, error messages, captured output, environment, and previous-result comparison.
- Only create queries for failures that appear backend-related or need backend evidence to confirm the likely cause.
- Do not query for purely client-side assertion failures when there is no backend signal.
- Treat resource provisioning timeouts, provisioningStatus/error state mismatches, API 5xx/4xx responses, trace IDs, request IDs, resource UUIDs, and cloud resource names as backend signals that justify a query.
- Use the exact failure_ref values from the input.
- test_name must match the input Test value for that failure_ref.
- expected_error must be the exact error message or shortest exact error signature from the failure evidence; leave it empty when there is no exact backend-looking error.
- search_terms must contain only identifiers, status codes, API error strings, resource names, or component names copied from the failure evidence.
- backend_area should name the likely backend component or area when the evidence supports one; otherwise use "unknown".
- reason must be one consolidated sentence explaining why this specific failure needs or does not need backend log evidence.
- confidence must be one of "high", "medium", or "low".
- Prefer precise IDs, request IDs, UUIDs, resource names, status codes, API error strings, and backend component names found in the failure evidence.
- For cross-component UI suites, do not assume a single backend component. Use a broad Kubernetes label selector such as {namespace=~".+"} unless the failure evidence clearly points to a narrower namespace or service.
- Keep each LogQL query readable and bounded for the supplied time window. Do not request writes or mutations.
- Do not include Grafana URLs in this JSON. The reporter generates grafana_explore_url deterministically after it knows the datasource, query, and time range.
- If no backend-related log lookup is justified, return {"queries":[]}.`
}

func unikornCRQueryPlanningPrompt() string {
	return `You are planning read-only Kubernetes custom-resource lookups for Unikorn test failure analysis.

Return strict JSON only. Do not include markdown, prose, code fences, shell scripts, or kubectl commands.

Expected output:
{"queries":[{"failure_ref":"f1","test_name":"creates network","backend_area":"network","resource":"networks.region.unikorn-cloud.org","namespace":"default","name":"network-123","selector":"","all_namespaces":false,"reason":"The test failed because a VPC/network resource reached Error and the failure includes the Network CR name, so the CR status can confirm the backend state.","confidence":"high"}]}

Rules:
- Inspect the failed test names, suites, locations, error messages, captured output, environment, and previous-result comparison.
- Do not use CRs as the default failure source. The suite report is primary; Grafana/logs and Claude should handle non-resource failures.
- Only create CR lookups for failures where Kubernetes custom-resource state could materially improve the failure analysis and the failure evidence contains a Unikorn/Kubernetes resource lifecycle signal.
- Do not create CR lookups for purely client-side assertions, local test framework failures, auth/token failures with no resource name, API validation errors, HTTP auth/config errors, generic 4xx/5xx responses without resource lifecycle evidence, or cleanup-only not_found errors unless the failure is about a Kubernetes-owned resource lifecycle.
- Treat provisioning timeouts, Error/Failed CR states, deleted/missing Kubernetes-owned resources, finalizers, owner references, cloud resource UUIDs, VPC/network/load balancer/instance/file storage resource names, and controller-owned custom resources as signals that can justify a lookup.
- Use the exact failure_ref values from the input.
- test_name must match the input Test value for that failure_ref.
- resource must be one kubectl resource identifier, preferably plural.group form such as networks.region.unikorn-cloud.org. Never include spaces, flags, pipes, or shell syntax.
- Supported resource examples for the github-unikorn-cr-reader bot include networks.region.unikorn-cloud.org, vlanallocations.region.unikorn-cloud.org, loadbalancers.region.unikorn-cloud.org, servers.region.unikorn-cloud.org, filestorages.region.unikorn-cloud.org, computeinstances.compute.unikorn-cloud.org, objectstorageendpoints.storage.unikorn-cloud.org, projects.identity.unikorn-cloud.org, and kubernetesclusters.unikorn-cloud.org.
- Include either name or selector. Prefer exact names, UUIDs, or resource names from the failure evidence.
- Set namespace when the evidence provides it. If namespace is unknown for a namespaced CR, set all_namespaces to true.
- backend_area should name the likely backend component or area when the evidence supports one; otherwise use "unknown".
- reason must be one consolidated sentence explaining why this specific failure needs Kubernetes CR state.
- confidence must be one of "high", "medium", or "low".
- The collector is read-only and only runs kubectl get/list on the requested CRs. Do not request pods, logs, exec, secrets, configmaps, events, writes, deletes, patches, or mutations.
- If no CR lookup is justified, return {"queries":[]}.`
}
