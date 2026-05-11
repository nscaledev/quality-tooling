# Uni Find Staging Constellation Action

Scans open PRs in the `uni-releases` repository for a constellation with `status: candidate` and outputs the pinned tag for the requested service. Used as a preflight step before UAT tests so they run against the version deployed to staging rather than HEAD of `main`.

## Requirements

Add a `RELEASES_READ_TOKEN` secret to your repository — a GitHub fine-grained token with read access to `nscaledev/uni-releases` (Contents + Pull Requests). The 1Password item is `uni-releases-read-token`.

## Usage

```yaml
find-staging-constellation:
  name: Find staging constellation
  runs-on: ubuntu-latest
  if: github.event_name == 'schedule' || github.event.inputs.run_uat == 'true'
  outputs:
    tag: ${{ steps.find.outputs.tag }}
  steps:
  - uses: nscaledev/quality-tooling/.github/actions/uni-find-staging-constellation@main
    id: find
    with:
      service: uni-region
      releases-read-token: ${{ secrets.RELEASES_READ_TOKEN }}

api-tests-uat:
  needs: find-staging-constellation
  if: needs.find-staging-constellation.outputs.tag != ''
  steps:
  - uses: actions/checkout@v4
    with:
      ref: ${{ needs.find-staging-constellation.outputs.tag }}
```

## Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `service` | Yes | - | Service name as it appears in the constellation manifest (e.g. `uni-region`, `uni-compute`) |
| `releases-read-token` | Yes | - | GitHub token with read access to the releases repository |
| `releases-repo` | No | `nscaledev/uni-releases` | Releases repository in `owner/repo` format |

## Outputs

| Output | Description |
|--------|-------------|
| `tag` | Git tag pinned in the candidate constellation (e.g. `v1.16.4`), empty if no candidate found |
