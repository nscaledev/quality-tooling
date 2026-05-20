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

	"gopkg.in/yaml.v3"
)

const githubAPI = "https://api.github.com"

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

// tagPattern validates the vX.Y.Z format we expect after stripping the short-SHA.
var tagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// errNotFound is returned by getJSON when the server responds with 404.
var errNotFound = errors.New("not found")

func main() {
	useStagingConstellation := !strings.EqualFold(strings.TrimSpace(os.Getenv("USE_STAGING_CONSTELLATION")), "false")

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

		if outputFile := os.Getenv("GITHUB_OUTPUT"); outputFile != "" {
			f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: failed to open GITHUB_OUTPUT: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()

			if _, err := fmt.Fprintf(f, "ref=%s\n", ref); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: failed to write GITHUB_OUTPUT: %v\n", err)
				os.Exit(1)
			}
		}

		return
	}

	service := os.Getenv("SERVICE")
	if service == "" {
		fmt.Fprintln(os.Stderr, "ERROR: SERVICE environment variable not set")
		os.Exit(1)
	}

	token := os.Getenv("GH_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "ERROR: GH_TOKEN environment variable not set")
		os.Exit(1)
	}

	releasesRepo := os.Getenv("RELEASES_REPO")
	if releasesRepo == "" {
		releasesRepo = "nscaledev/uni-releases"
	}

	client := &githubClient{token: token}

	tag, err := findCandidateTag(client, releasesRepo, service)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	if tag == "" {
		fmt.Println("No candidate constellation in staging — UAT tests will be skipped")
		return
	}

	if outputFile := os.Getenv("GITHUB_OUTPUT"); outputFile != "" {
		f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: failed to open GITHUB_OUTPUT: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		if _, err := fmt.Fprintf(f, "tag=%s\n", tag); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: failed to write GITHUB_OUTPUT: %v\n", err)
			os.Exit(1)
		}

		if _, err := fmt.Fprintf(f, "ref=%s\n", tag); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: failed to write GITHUB_OUTPUT: %v\n", err)
			os.Exit(1)
		}
	}
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

	nextURL := fmt.Sprintf("%s/repos/%s/pulls?state=open&per_page=100", githubAPI, repo)

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
	u := fmt.Sprintf("%s/repos/%s/contents/constellations?ref=%s", githubAPI, repo, url.QueryEscape(ref))

	var files []fileEntry
	if _, err := client.getJSON(u, &files); err != nil {
		return nil, err
	}

	return files, nil
}

// fetchFileContent downloads a single file and returns its decoded content.
func fetchFileContent(client *githubClient, repo, ref, filename string) ([]byte, error) {
	u := fmt.Sprintf("%s/repos/%s/contents/constellations/%s?ref=%s", githubAPI, repo, filename, url.QueryEscape(ref))

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
