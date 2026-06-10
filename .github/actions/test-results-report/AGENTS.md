# test-results-report Agent Rules

These rules apply when editing the `test-results-report` GitHub Action.

## Report Shape

- Keep GitHub step summaries close to the current production format.
- Add Grafana/Loki as a compact `### Grafana Observations` section only.
- Do not put raw Loki rows, LogQL blocks, search terms, exact failure metadata, or Grafana debug traces in the GitHub summary.
- Keep raw or detailed Grafana evidence out of the user-facing summary; pass only compact observations to the final AI report.
- If Grafana/Loki evidence is used in AI analysis, the `Likely reason` or `Next check` must mention a concrete, relevant signal.
- Slack must omit weak, time-disjoint, identifier-unmatched, or likely unrelated Grafana observations instead of explaining that they are probably unrelated.
- Classify skipped tests as `skipped`; do not label intentional skips as `test/false failure`.

## Grafana MCP Logic

- Let AI planning decide whether a failed test is backend-related before querying Grafana MCP.
- Do not query Loki for purely client-side assertions unless the failure evidence contains a backend signal.
- Prefer exact identifiers from test artifacts: request IDs, trace IDs, UUIDs, resource names, status codes, API error strings, and backend component names.
- Execute per-failure Grafana MCP queries in parallel, bounded by `grafana-log-concurrency`.
- Keep Grafana defaults centralized at the source (`config.go` / action wrapper), not repeated at each caller.

## AI Input

- Feed Claude compact `Grafana/Loki observations for final analysis`.
- Include datasource/time range, backend area, line count, matched components, first-match timestamp/component, and a compact Loki signal.
- Do not include raw log-line bodies, LogQL, search terms, exact failure error blocks, or Grafana URLs in Claude input unless explicitly debugging the action internals.
- Preserve existing failed/skipped test context unless the user explicitly asks to reduce it.

## Verification

- Run `go test ./...` after action logic or rendering changes.
- Run `git diff --check` before finishing.
- For reporting changes, replay a real failed artifact locally with mocked Claude/MCP when live secrets are unavailable.
- For live E2E validation, run in GitHub Actions where Grafana and Claude secrets are available.

## Branch Hygiene

- Check `git status --short --branch` before edits and before finishing.
- Keep unrelated generated files, schema churn, and cross-repo changes out of this action branch unless explicitly requested.
- When updating downstream repos (`uni-region`, `uni-compute`, `nscale-ui`), verify each branch diff is scoped to workflow/action integration before committing or pushing.
