/*
Copyright 2026 Nscale.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultGitHubAPI = "https://api.github.com"

var versionAPIRequestTimeout = 15 * time.Second

// pullRequest is the subset of the GitHub pulls API response we need.
type pullRequest struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

// fileEntry is a single item from the GitHub contents directory listing.
type fileEntry struct {
	Name string `json:"name"`
}

// fileContent is the GitHub contents API response for a single file.
type fileContent struct {
	Content string `json:"content"`
}

// constellation is the subset of the constellation YAML we care about.
type constellation struct {
	Status   string                          `yaml:"status"`
	Services map[string]constellationService `yaml:"services"`
}

type constellationService struct {
	Version string `yaml:"version"`
}

type serviceVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type versionAPIConfig struct {
	versionURL   string
	versionToken string
	serviceRepo  string
	repoToken    string
}

type versionAPIRef struct {
	tag     string
	ref     string
	version string
}

// tagPattern validates the vX.Y.Z format we expect after stripping the short-SHA.
var tagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// semverTagPattern validates service versions that should exist as Git tags.
var semverTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?(?:\+[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// pseudoVersionPattern validates Go pseudo-versions and captures the commit hash.
var pseudoVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:[0-9A-Za-z][0-9A-Za-z.-]*\.)?\d{14}-([0-9a-f]{12,40})$`)

// errNotFound is returned by getJSON when the server responds with 404.
var errNotFound = errors.New("not found")

func main() {
	useStagingConstellation := !strings.EqualFold(strings.TrimSpace(os.Getenv("USE_STAGING_CONSTELLATION")), "false")
	useVersionAPI := envBool("USE_VERSION_API", false)
	fallbackToConstellation := envBool("FALLBACK_TO_CONSTELLATION", true)

	if !useStagingConstellation {
		if eventName := os.Getenv("GITHUB_EVENT_NAME"); eventName != "workflow_dispatch" {
			fmt.Fprintln(os.Stderr, "ERROR: USE_STAGING_CONSTELLATION=false is only supported for workflow_dispatch runs")
			os.Exit(1)
		}

		ref := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME"))
		if ref == "" {
			fmt.Fprintln(os.Stderr, "ERROR: GITHUB_REF_NAME environment variable not set")
			os.Exit(1)
		}

		fmt.Printf("Using selected workflow ref for UAT: %s\n", ref)

		if err := writeOutputs("", ref, ""); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}

		return
	}

	service := os.Getenv("SERVICE")
	if service == "" {
		fmt.Fprintln(os.Stderr, "ERROR: SERVICE environment variable not set")
		os.Exit(1)
	}

	releasesRepo := os.Getenv("RELEASES_REPO")
	if releasesRepo == "" {
		releasesRepo = "nscaledev/uni-releases"
	}

	releasesToken := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	repoToken := firstNonEmpty(os.Getenv("SERVICE_REPO_TOKEN"), releasesToken)

	if useVersionAPI {
		resolved, err := findVersionAPIRef(&githubClient{token: repoToken}, versionAPIConfig{
			versionURL:   strings.TrimSpace(os.Getenv("VERSION_API_URL")),
			versionToken: strings.TrimSpace(os.Getenv("VERSION_API_TOKEN")),
			serviceRepo:  firstNonEmpty(os.Getenv("SERVICE_REPO"), os.Getenv("GITHUB_REPOSITORY")),
			repoToken:    repoToken,
		})
		if err == nil {
			if err := writeOutputs(resolved.tag, resolved.ref, resolved.version); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				os.Exit(1)
			}

			return
		}

		if !fallbackToConstellation {
			fmt.Fprintf(os.Stderr, "ERROR: version API lookup failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("WARNING: version API lookup failed: %v; falling back to staged constellation\n", err)
	}

	if releasesToken == "" {
		fmt.Fprintln(os.Stderr, "ERROR: GH_TOKEN environment variable not set")
		os.Exit(1)
	}

	client := &githubClient{token: releasesToken}

	tag, err := findCandidateTag(client, releasesRepo, service)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	if tag == "" {
		fmt.Println("No candidate constellation in staging — UAT tests will be skipped")
		return
	}

	if err := writeOutputs(tag, tag, ""); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func writeOutputs(tag, ref, version string) error {
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile == "" {
		return nil
	}

	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open GITHUB_OUTPUT: %w", err)
	}
	defer f.Close()

	if tag != "" {
		if _, err := fmt.Fprintf(f, "tag=%s\n", tag); err != nil {
			return fmt.Errorf("failed to write GITHUB_OUTPUT: %w", err)
		}
	}

	if ref != "" {
		if _, err := fmt.Fprintf(f, "ref=%s\n", ref); err != nil {
			return fmt.Errorf("failed to write GITHUB_OUTPUT: %w", err)
		}
	}

	if version != "" {
		if _, err := fmt.Fprintf(f, "version=%s\n", version); err != nil {
			return fmt.Errorf("failed to write GITHUB_OUTPUT: %w", err)
		}
	}

	return nil
}

func envBool(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}

	return strings.EqualFold(raw, "true")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}

	return ""
}

func githubAPIURL() string {
	if raw := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_API_URL")), "/"); raw != "" {
		return raw
	}

	return defaultGitHubAPI
}

func findVersionAPIRef(client *githubClient, cfg versionAPIConfig) (versionAPIRef, error) {
	versionURL := strings.TrimSpace(cfg.versionURL)
	if versionURL == "" {
		return versionAPIRef{}, errors.New("VERSION_API_URL environment variable not set") //nolint:err113
	}

	if cfg.serviceRepo == "" {
		return versionAPIRef{}, errors.New("SERVICE_REPO or GITHUB_REPOSITORY environment variable not set") //nolint:err113
	}

	if cfg.repoToken == "" {
		return versionAPIRef{}, errors.New("SERVICE_REPO_TOKEN or GH_TOKEN environment variable not set") //nolint:err113
	}

	serviceVersion, err := fetchServiceVersion(versionURL, cfg.versionToken)
	if err != nil {
		return versionAPIRef{}, err
	}

	version := strings.TrimSpace(serviceVersion.Version)
	if commitRef := pseudoVersionCommit(version); commitRef != "" {
		sha, err := resolveCommit(client, cfg.serviceRepo, commitRef)
		if err != nil {
			return versionAPIRef{}, fmt.Errorf("checking pseudo-version commit %s in %s: %w", commitRef, cfg.serviceRepo, err)
		}

		fmt.Printf("Matched service version API: %s reports pseudo-version %s (%s); commit %s exists in %s\n", versionURL, version, serviceVersion.Name, sha, cfg.serviceRepo)

		return versionAPIRef{ref: sha, version: version}, nil
	}

	if !semverTagPattern.MatchString(version) {
		return versionAPIRef{}, fmt.Errorf("version API returned %q, which does not match expected semver tag or Go pseudo-version format", serviceVersion.Version) //nolint:err113
	}

	exists, err := tagExists(client, cfg.serviceRepo, version)
	if err != nil {
		return versionAPIRef{}, fmt.Errorf("checking tag %s in %s: %w", version, cfg.serviceRepo, err)
	}

	if !exists {
		return versionAPIRef{}, fmt.Errorf("tag %s from version API does not exist in %s", version, cfg.serviceRepo) //nolint:err113
	}

	fmt.Printf("Matched service version API: %s reports %s (%s); tag exists in %s\n", versionURL, version, serviceVersion.Name, cfg.serviceRepo)

	return versionAPIRef{tag: version, ref: version, version: version}, nil
}

func pseudoVersionCommit(version string) string {
	matches := pseudoVersionPattern.FindStringSubmatch(version)
	if len(matches) != 2 {
		return ""
	}

	return matches[1]
}

func fetchServiceVersion(versionURL, token string) (serviceVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionAPIRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return serviceVersion{}, fmt.Errorf("creating request for %s: %w", versionURL, err)
	}

	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return serviceVersion{}, fmt.Errorf("GET %s: %w", versionURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return serviceVersion{}, fmt.Errorf("reading response from %s: %w", versionURL, err)
	}

	if resp.StatusCode >= 400 {
		return serviceVersion{}, fmt.Errorf("GET %s returned %d: %s", versionURL, resp.StatusCode, string(body)) //nolint:err113
	}

	var version serviceVersion
	if err := json.Unmarshal(body, &version); err != nil {
		return serviceVersion{}, fmt.Errorf("decoding response from %s: %w", versionURL, err)
	}

	if version.Version == "" {
		return serviceVersion{}, fmt.Errorf("version API response from %s did not include version", versionURL) //nolint:err113
	}

	return version, nil
}

func tagExists(client *githubClient, repo, tag string) (bool, error) {
	u := fmt.Sprintf("%s/repos/%s/git/ref/tags/%s", githubAPIURL(), repo, url.PathEscape(tag))

	var ref struct {
		Ref string `json:"ref"`
	}

	if _, err := client.getJSON(u, &ref); err != nil {
		if errors.Is(err, errNotFound) {
			return false, nil
		}

		return false, err
	}

	return ref.Ref != "", nil
}

func resolveCommit(client *githubClient, repo, ref string) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/commits/%s", githubAPIURL(), repo, url.PathEscape(ref))

	var commit struct {
		SHA string `json:"sha"`
	}

	if _, err := client.getJSON(u, &commit); err != nil {
		return "", err
	}

	if commit.SHA == "" {
		return "", fmt.Errorf("commit lookup for %s in %s did not include a SHA", ref, repo) //nolint:err113
	}

	return commit.SHA, nil
}

// findCandidateTag scans open PRs in releasesRepo for a constellation with
// status=candidate or status=released and returns the plain vX.Y.Z tag for
// the given service. Returns an empty string (not an error) when none is found.
func findCandidateTag(client *githubClient, releasesRepo, service string) (string, error) {
	prs, err := listOpenPRs(client, releasesRepo)
	if err != nil {
		return "", fmt.Errorf("listing open PRs: %w", err)
	}

	for _, pr := range prs {
		tag, err := scanPR(client, releasesRepo, service, pr)
		if err != nil {
			return "", err
		}

		if tag != "" {
			return tag, nil
		}
	}

	return "", nil
}

// scanPR checks a single PR for a candidate constellation containing the service.
func scanPR(client *githubClient, releasesRepo, service string, pr pullRequest) (string, error) {
	files, err := listConstellationFiles(client, releasesRepo, pr.Head.SHA)
	if errors.Is(err, errNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("PR #%d: listing constellations: %w", pr.Number, err)
	}

	for _, f := range files {
		tag, err := checkConstellationFile(client, releasesRepo, service, pr, f.Name)
		if err != nil {
			return "", err
		}

		if tag != "" {
			return tag, nil
		}
	}

	return "", nil
}

// checkConstellationFile downloads and parses a single constellation file.
// Returns the resolved tag if this file is a candidate for the service, or empty string to continue.
func checkConstellationFile(client *githubClient, releasesRepo, service string, pr pullRequest, filename string) (string, error) {
	raw, err := fetchFileContent(client, releasesRepo, pr.Head.SHA, filename)
	if errors.Is(err, errNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("PR #%d: fetching %s: %w", pr.Number, filename, err)
	}

	var c constellation
	if err := yaml.Unmarshal(raw, &c); err != nil {
		fmt.Printf("WARNING: PR #%d: skipping %s: malformed YAML: %v\n", pr.Number, filename, err)
		return "", nil
	}

	if c.Status != "candidate" && c.Status != "released" {
		return "", nil
	}

	svc, ok := c.Services[service]
	if !ok || svc.Version == "" {
		return "", fmt.Errorf("constellation found (PR #%d, %s, status=%s) but %q version is missing or null", pr.Number, filename, c.Status, service) //nolint:err113
	}

	// Strip trailing short-SHA: v1.16.4-c2153ee -> v1.16.4
	tag, _, _ := strings.Cut(svc.Version, "-")

	if !tagPattern.MatchString(tag) {
		return "", fmt.Errorf("extracted tag %q from %q does not match expected vX.Y.Z format", tag, svc.Version) //nolint:err113
	}

	fmt.Printf("Matched constellation: PR #%d (%s), status=%s, file %s, tag %s\n", pr.Number, pr.Head.Ref, c.Status, filename, tag)

	return tag, nil
}

// listOpenPRs returns all open pull requests in the repository, following pagination.
func listOpenPRs(client *githubClient, repo string) ([]pullRequest, error) {
	var all []pullRequest

	nextURL := fmt.Sprintf("%s/repos/%s/pulls?state=open&per_page=100", githubAPIURL(), repo)

	for nextURL != "" {
		var page []pullRequest

		next, err := client.getJSON(nextURL, &page)
		if err != nil {
			return nil, err
		}

		all = append(all, page...)
		nextURL = next
	}

	return all, nil
}

// listConstellationFiles returns the files in the constellations/ directory at the given ref.
func listConstellationFiles(client *githubClient, repo, ref string) ([]fileEntry, error) {
	u := fmt.Sprintf("%s/repos/%s/contents/constellations?ref=%s", githubAPIURL(), repo, url.QueryEscape(ref))

	var files []fileEntry
	if _, err := client.getJSON(u, &files); err != nil {
		return nil, err
	}

	return files, nil
}

// fetchFileContent downloads a single file and returns its decoded content.
func fetchFileContent(client *githubClient, repo, ref, filename string) ([]byte, error) {
	u := fmt.Sprintf("%s/repos/%s/contents/constellations/%s?ref=%s", githubAPIURL(), repo, filename, url.QueryEscape(ref))

	var fc fileContent
	if _, err := client.getJSON(u, &fc); err != nil {
		return nil, err
	}

	// GitHub base64-encodes file content with newlines every 60 chars.
	clean := strings.ReplaceAll(fc.Content, "\n", "")

	return base64.StdEncoding.DecodeString(clean)
}

// githubClient is a minimal GitHub API client.
type githubClient struct {
	token string
}

// getJSON makes a GET request, decodes the JSON body into v, and returns the
// URL of the next page from the Link header (empty string if none).
func (c *githubClient) getJSON(rawURL string, v any) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request for %s: %w", rawURL, err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", rawURL, err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", errNotFound
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("GET %s returned %d: %s", rawURL, resp.StatusCode, string(body)) //nolint:err113
	}

	if err := json.Unmarshal(body, v); err != nil {
		return "", fmt.Errorf("decoding response from %s: %w", rawURL, err)
	}

	return parseLinkNext(resp.Header.Get("Link")), nil
}

// parseLinkNext extracts the URL with rel="next" from a GitHub Link header.
func parseLinkNext(link string) string {
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")

		if len(segments) != 2 {
			continue
		}

		if strings.TrimSpace(segments[1]) == `rel="next"` {
			u := strings.TrimSpace(segments[0])
			return strings.Trim(u, "<>")
		}
	}

	return ""
}
