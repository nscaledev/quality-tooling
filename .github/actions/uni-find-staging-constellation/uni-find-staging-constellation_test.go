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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
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
