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
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSelectedWorkflowRefWritesRefOutput(t *testing.T) {
	outputFile := createOutputFile(t)

	result := runMain(t, map[string]string{
		"USE_STAGING_CONSTELLATION": " FALSE ",
		"GITHUB_EVENT_NAME":         "workflow_dispatch",
		"GITHUB_REF_NAME":           "feature/test-uat",
		"GITHUB_OUTPUT":             outputFile,
	})

	if result.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stdout, "Using selected workflow ref for UAT: feature/test-uat") {
		t.Fatalf("expected selected ref log, got stdout:\n%s", result.stdout)
	}

	output := readFile(t, outputFile)
	if output != "ref=feature/test-uat\n" {
		t.Fatalf("expected only ref output, got %q", output)
	}
}

func TestSelectedWorkflowRefRequiresGitHubRefName(t *testing.T) {
	result := runMain(t, map[string]string{
		"USE_STAGING_CONSTELLATION": "false",
		"GITHUB_EVENT_NAME":         "workflow_dispatch",
	})

	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stderr, "ERROR: GITHUB_REF_NAME environment variable not set") {
		t.Fatalf("expected missing GITHUB_REF_NAME error, got stderr:\n%s", result.stderr)
	}
}

func TestSelectedWorkflowRefRequiresWorkflowDispatch(t *testing.T) {
	result := runMain(t, map[string]string{
		"USE_STAGING_CONSTELLATION": "false",
		"GITHUB_EVENT_NAME":         "pull_request",
		"GITHUB_REF_NAME":           "42/merge",
	})

	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stderr, "ERROR: USE_STAGING_CONSTELLATION=false is only supported for workflow_dispatch runs") {
		t.Fatalf("expected workflow_dispatch guard error, got stderr:\n%s", result.stderr)
	}
}

func TestUnsetUseStagingConstellationUsesStagingLookup(t *testing.T) {
	result := runMain(t, map[string]string{})

	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stderr, "ERROR: SERVICE environment variable not set") {
		t.Fatalf("expected staging lookup path to require SERVICE, got stderr:\n%s", result.stderr)
	}
}

func TestVersionAPIWritesTagAndRefOutput(t *testing.T) {
	outputFile := createOutputFile(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			if got := r.Header.Get("Authorization"); got != "Bearer api-token" {
				t.Errorf("expected version API bearer token, got %q", got)
			}
			fmt.Fprint(w, `{"name":"unikorn-region-server","version":"v1.17.2"}`)
		case "/repos/nscaledev/uni-region/git/ref/tags/v1.17.2":
			if got := r.Header.Get("Authorization"); got != "Bearer repo-token" {
				t.Errorf("expected repo token, got %q", got)
			}
			fmt.Fprint(w, `{"ref":"refs/tags/v1.17.2"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result := runMain(t, map[string]string{
		"SERVICE":            "uni-region",
		"USE_VERSION_API":    "true",
		"VERSION_API_URL":    server.URL + "/api/version",
		"VERSION_API_TOKEN":  "api-token",
		"SERVICE_REPO":       "nscaledev/uni-region",
		"SERVICE_REPO_TOKEN": "repo-token",
		"GITHUB_API_URL":     server.URL,
		"GITHUB_OUTPUT":      outputFile,
	})

	if result.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stdout, "Matched service version API") {
		t.Fatalf("expected version API match log, got stdout:\n%s", result.stdout)
	}

	output := readFile(t, outputFile)
	if output != "tag=v1.17.2\nref=v1.17.2\n" {
		t.Fatalf("expected tag and ref output, got %q", output)
	}
}

func TestVersionAPIWritesPseudoVersionCommitRefOutput(t *testing.T) {
	outputFile := createOutputFile(t)
	const fullSHA = "517a48e78688ea507b64831e0aaae0ad4a78f43c"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			fmt.Fprint(w, `{"name":"unikorn-region-server","version":"v0.0.0-20260625031624-517a48e78688"}`)
		case "/repos/nscaledev/uni-region/commits/517a48e78688":
			if got := r.Header.Get("Authorization"); got != "Bearer repo-token" {
				t.Errorf("expected repo token, got %q", got)
			}
			fmt.Fprintf(w, `{"sha":%q}`, fullSHA)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result := runMain(t, map[string]string{
		"SERVICE":            "uni-region",
		"USE_VERSION_API":    "true",
		"VERSION_API_URL":    server.URL + "/api/version",
		"SERVICE_REPO":       "nscaledev/uni-region",
		"SERVICE_REPO_TOKEN": "repo-token",
		"GITHUB_API_URL":     server.URL,
		"GITHUB_OUTPUT":      outputFile,
	})

	if result.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stdout, "reports pseudo-version v0.0.0-20260625031624-517a48e78688") {
		t.Fatalf("expected pseudo-version match log, got stdout:\n%s", result.stdout)
	}

	output := readFile(t, outputFile)
	if output != "ref="+fullSHA+"\n" {
		t.Fatalf("expected commit ref output, got %q", output)
	}
}

func TestVersionAPIFallsBackToConstellation(t *testing.T) {
	outputFile := createOutputFile(t)
	const constellationYAML = `status: candidate
services:
  uni-region:
    version: v1.16.4-c2153ee
`
	encodedConstellation := base64.StdEncoding.EncodeToString([]byte(constellationYAML))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			http.NotFound(w, r)
		case "/repos/nscaledev/uni-releases/pulls":
			fmt.Fprint(w, `[{"number":42,"head":{"sha":"abc123","ref":"release/staging"}}]`)
		case "/repos/nscaledev/uni-releases/contents/constellations":
			fmt.Fprint(w, `[{"name":"candidate.yaml"}]`)
		case "/repos/nscaledev/uni-releases/contents/constellations/candidate.yaml":
			fmt.Fprintf(w, `{"content":%q}`, encodedConstellation)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result := runMain(t, map[string]string{
		"SERVICE":            "uni-region",
		"GH_TOKEN":           "releases-token",
		"RELEASES_REPO":      "nscaledev/uni-releases",
		"USE_VERSION_API":    "true",
		"VERSION_API_URL":    server.URL + "/api/version",
		"SERVICE_REPO":       "nscaledev/uni-region",
		"SERVICE_REPO_TOKEN": "repo-token",
		"GITHUB_API_URL":     server.URL,
		"GITHUB_OUTPUT":      outputFile,
	})

	if result.exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stdout, "falling back to staged constellation") {
		t.Fatalf("expected fallback log, got stdout:\n%s", result.stdout)
	}

	output := readFile(t, outputFile)
	if output != "tag=v1.16.4\nref=v1.16.4\n" {
		t.Fatalf("expected fallback tag and ref output, got %q", output)
	}
}

func TestVersionAPIStrictModeFailsWithoutFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	result := runMain(t, map[string]string{
		"SERVICE":                   "uni-region",
		"USE_VERSION_API":           "true",
		"VERSION_API_URL":           server.URL + "/api/version",
		"FALLBACK_TO_CONSTELLATION": "false",
		"SERVICE_REPO":              "nscaledev/uni-region",
		"SERVICE_REPO_TOKEN":        "repo-token",
		"GITHUB_API_URL":            server.URL,
	})

	if result.exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d\nstdout:\n%s\nstderr:\n%s", result.exitCode, result.stdout, result.stderr)
	}

	if !strings.Contains(result.stderr, "ERROR: version API lookup failed") {
		t.Fatalf("expected version API lookup error, got stderr:\n%s", result.stderr)
	}
}

func TestFetchServiceVersionTimesOut(t *testing.T) {
	originalTimeout := versionAPIRequestTimeout
	versionAPIRequestTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		versionAPIRequestTimeout = originalTimeout
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		fmt.Fprint(w, `{"name":"unikorn-region-server","version":"v1.17.2"}`)
	}))
	defer server.Close()

	_, err := fetchServiceVersion(server.URL+"/api/version", "")
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected deadline exceeded error, got %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	main()
	os.Exit(0)
}

type mainResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func runMain(t *testing.T, env map[string]string) mainResult {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")

	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return mainResult{
			exitCode: 0,
			stdout:   stdout.String(),
			stderr:   stderr.String(),
		}
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running helper process: %v", err)
	}

	return mainResult{
		exitCode: exitErr.ExitCode(),
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func createOutputFile(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "github-output")
	if err != nil {
		t.Fatalf("creating output file: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("closing output file: %v", err)
	}

	return f.Name()
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}
