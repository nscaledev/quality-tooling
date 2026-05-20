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
	"strings"
	"testing"
	"time"
)

// --- parseGinkgoReport ---

func TestParseGinkgoReport(t *testing.T) {
	t.Run("all passing", func(t *testing.T) {
		data := []byte(`[{
			"SuiteDescription": "My Suite",
			"SuiteSucceeded": true,
			"StartTime": "2026-05-20T10:00:00Z",
			"RunTime": 2000000000,
			"SpecReports": [
				{"State": "passed", "ContainerHierarchyTexts": ["A"], "LeafNodeText": "passes"},
				{"State": "skipped", "ContainerHierarchyTexts": ["A"], "LeafNodeText": "skipped"}
			]
		}]`)

		r, err := parseGinkgoReport(data)
		if err != nil {
			t.Fatal(err)
		}

		if r.suiteName != "My Suite" {
			t.Errorf("suiteName = %q, want %q", r.suiteName, "My Suite")
		}
		if !r.succeeded {
			t.Error("succeeded should be true")
		}
		if r.stats.passed != 1 || r.stats.failed != 0 || r.stats.skipped != 1 || r.stats.total != 2 {
			t.Errorf("stats = %+v, want passed=1 failed=0 skipped=1 total=2", r.stats)
		}
		if len(r.failures) != 0 {
			t.Errorf("failures = %d, want 0", len(r.failures))
		}
		if r.duration != 2*time.Second {
			t.Errorf("duration = %v, want 2s", r.duration)
		}
	})

	t.Run("with failure", func(t *testing.T) {
		data := []byte(`[{
			"SuiteDescription": "My Suite",
			"SuiteSucceeded": false,
			"StartTime": "2026-05-20T10:00:00Z",
			"RunTime": 1000000000,
			"SpecReports": [
				{
					"State": "failed",
					"ContainerHierarchyTexts": ["Suite", "Sub"],
					"LeafNodeText": "should work",
					"Failure": {
						"Message": "expected true got false",
						"Location": {"FileName": "/path/to/foo_test.go", "LineNumber": 99}
					},
					"CapturedGinkgoWriterOutput": "some output"
				}
			]
		}]`)

		r, err := parseGinkgoReport(data)
		if err != nil {
			t.Fatal(err)
		}

		if r.succeeded {
			t.Error("succeeded should be false")
		}
		if r.stats.failed != 1 || r.stats.total != 1 {
			t.Errorf("stats = %+v, want failed=1 total=1", r.stats)
		}
		if len(r.failures) != 1 {
			t.Fatalf("failures = %d, want 1", len(r.failures))
		}

		f := r.failures[0]
		if f.testName != "Suite > Sub > should work" {
			t.Errorf("testName = %q", f.testName)
		}
		if f.location != "foo_test.go:99" {
			t.Errorf("location = %q, want %q", f.location, "foo_test.go:99")
		}
		if f.errorMsg != "expected true got false" {
			t.Errorf("errorMsg = %q", f.errorMsg)
		}
		if f.output != "some output" {
			t.Errorf("output = %q", f.output)
		}
	})

	t.Run("error message truncated at 500 chars", func(t *testing.T) {
		msg := strings.Repeat("x", 600)
		data := []byte(`[{"SuiteDescription":"S","SuiteSucceeded":false,"StartTime":"2026-05-20T10:00:00Z","RunTime":0,"SpecReports":[{"State":"failed","ContainerHierarchyTexts":[],"LeafNodeText":"t","Failure":{"Message":"` + msg + `","Location":{"FileName":"f.go","LineNumber":1}}}]}]`)

		r, err := parseGinkgoReport(data)
		if err != nil {
			t.Fatal(err)
		}

		if len(r.failures) == 0 {
			t.Fatal("expected 1 failure")
		}
		if !strings.HasSuffix(r.failures[0].errorMsg, "...") {
			t.Error("errorMsg should end with ...")
		}
		if len(r.failures[0].errorMsg) != 503 {
			t.Errorf("errorMsg length = %d, want 503 (500 + ...)", len(r.failures[0].errorMsg))
		}
	})

	t.Run("output truncated at 300 chars", func(t *testing.T) {
		output := strings.Repeat("y", 400)
		data := []byte(`[{"SuiteDescription":"S","SuiteSucceeded":false,"StartTime":"2026-05-20T10:00:00Z","RunTime":0,"SpecReports":[{"State":"failed","ContainerHierarchyTexts":[],"LeafNodeText":"t","CapturedGinkgoWriterOutput":"` + output + `","Failure":{"Message":"err","Location":{"FileName":"f.go","LineNumber":1}}}]}]`)

		r, err := parseGinkgoReport(data)
		if err != nil {
			t.Fatal(err)
		}

		if len(r.failures) == 0 {
			t.Fatal("expected 1 failure")
		}
		if len(r.failures[0].output) != 303 {
			t.Errorf("output length = %d, want 303 (300 + ...)", len(r.failures[0].output))
		}
	})

	t.Run("empty reports returns error", func(t *testing.T) {
		_, err := parseGinkgoReport([]byte(`[]`))
		if err == nil {
			t.Error("expected error for empty reports")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := parseGinkgoReport([]byte(`not json`))
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

// --- parseJUnitReport ---

func TestParseJUnitReport(t *testing.T) {
	t.Run("single suite", func(t *testing.T) {
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="suite" tests="3" failures="1" errors="0" skipped="1" time="5.0" timestamp="2026-05-20T10:00:00">
    <testcase classname="pkg.A" name="passes" />
    <testcase classname="pkg.A" name="fails"><failure message="bad">detail</failure></testcase>
    <testcase classname="pkg.A" name="skips"><skipped/></testcase>
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}

		if r.succeeded {
			t.Error("succeeded should be false")
		}
		if r.stats.total != 3 || r.stats.passed != 1 || r.stats.failed != 1 || r.stats.skipped != 1 {
			t.Errorf("stats = %+v, want total=3 passed=1 failed=1 skipped=1", r.stats)
		}
		if r.duration != 5*time.Second {
			t.Errorf("duration = %v, want 5s", r.duration)
		}
		if len(r.failures) != 1 {
			t.Fatalf("failures = %d, want 1", len(r.failures))
		}
		if r.failures[0].testName != "pkg.A > fails" {
			t.Errorf("testName = %q", r.failures[0].testName)
		}
		if r.failures[0].errorMsg != "detail" {
			t.Errorf("errorMsg = %q, want %q", r.failures[0].errorMsg, "detail")
		}
	})

	t.Run("multiple suites aggregated", func(t *testing.T) {
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="suite-a" tests="3" failures="1" errors="0" skipped="0" time="2.0" timestamp="2026-05-20T10:00:00">
    <testcase classname="a" name="pass1" />
    <testcase classname="a" name="pass2" />
    <testcase classname="a" name="fail1"><failure message="e1">detail1</failure></testcase>
  </testsuite>
  <testsuite name="suite-b" tests="2" failures="1" errors="0" skipped="1" time="3.0" timestamp="2026-05-20T10:01:00">
    <testcase classname="b" name="pass3" />
    <testcase classname="b" name="fail2"><failure message="e2">detail2</failure></testcase>
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}

		if r.stats.total != 5 {
			t.Errorf("total = %d, want 5", r.stats.total)
		}
		if r.stats.passed != 2 {
			t.Errorf("passed = %d, want 2", r.stats.passed)
		}
		if r.stats.failed != 2 {
			t.Errorf("failed = %d, want 2", r.stats.failed)
		}
		if r.stats.skipped != 1 {
			t.Errorf("skipped = %d, want 1", r.stats.skipped)
		}
		if r.duration != 5*time.Second {
			t.Errorf("duration = %v, want 5s (sum of both suites)", r.duration)
		}
		// startTime should be the earliest timestamp (suite-a at 10:00:00)
		wantStart, _ := time.Parse("2006-01-02T15:04:05", "2026-05-20T10:00:00")
		if !r.startTime.Equal(wantStart) {
			t.Errorf("startTime = %v, want %v", r.startTime, wantStart)
		}
		if len(r.failures) != 2 {
			t.Fatalf("failures = %d, want 2 (one from each suite)", len(r.failures))
		}
		if r.failures[0].testName != "a > fail1" || r.failures[1].testName != "b > fail2" {
			t.Errorf("failure names = [%q, %q]", r.failures[0].testName, r.failures[1].testName)
		}
	})

	t.Run("error element treated as failure", func(t *testing.T) {
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="s" tests="1" failures="0" errors="1" skipped="0" time="1.0" timestamp="2026-05-20T10:00:00">
    <testcase classname="pkg" name="t1"><error message="conn refused">socket error</error></testcase>
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}

		if r.succeeded {
			t.Error("succeeded should be false")
		}
		if r.stats.failed != 1 {
			t.Errorf("failed = %d, want 1", r.stats.failed)
		}
		if len(r.failures) != 1 {
			t.Fatalf("failures = %d, want 1", len(r.failures))
		}
		if r.failures[0].errorMsg != "socket error" {
			t.Errorf("errorMsg = %q", r.failures[0].errorMsg)
		}
	})

	t.Run("falls back to message attr when text is empty", func(t *testing.T) {
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="s" tests="1" failures="1" errors="0" skipped="0" time="1.0" timestamp="2026-05-20T10:00:00">
    <testcase classname="pkg" name="t1"><failure message="only in attr"></failure></testcase>
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}

		if r.failures[0].errorMsg != "only in attr" {
			t.Errorf("errorMsg = %q, want message attr fallback", r.failures[0].errorMsg)
		}
	})

	t.Run("error message truncated at 500 chars", func(t *testing.T) {
		long := strings.Repeat("z", 600)
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="s" tests="1" failures="1" errors="0" skipped="0" time="1.0" timestamp="2026-05-20T10:00:00">
    <testcase classname="pkg" name="t1"><failure message="m">` + long + `</failure></testcase>
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}

		if len(r.failures) == 0 {
			t.Fatal("expected 1 failure")
		}
		if len(r.failures[0].errorMsg) != 503 {
			t.Errorf("errorMsg length = %d, want 503 (500 + ...)", len(r.failures[0].errorMsg))
		}
		if !strings.HasSuffix(r.failures[0].errorMsg, "...") {
			t.Error("errorMsg should end with ...")
		}
	})

	t.Run("invalid timestamp falls back to now", func(t *testing.T) {
		before := time.Now()
		xml := []byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="s" tests="1" failures="0" errors="0" skipped="0" time="1.0" timestamp="not-a-date">
    <testcase classname="pkg" name="t1" />
  </testsuite>
</testsuites>`)

		r, err := parseJUnitReport(xml)
		if err != nil {
			t.Fatal(err)
		}
		after := time.Now()

		if r.startTime.Before(before) || r.startTime.After(after) {
			t.Errorf("startTime %v not between %v and %v", r.startTime, before, after)
		}
	})

	t.Run("no suites returns error", func(t *testing.T) {
		_, err := parseJUnitReport([]byte(`<?xml version="1.0"?><testsuites></testsuites>`))
		if err == nil {
			t.Error("expected error for empty testsuites")
		}
	})

	t.Run("invalid XML returns error", func(t *testing.T) {
		_, err := parseJUnitReport([]byte(`not xml`))
		if err == nil {
			t.Error("expected error for invalid XML")
		}
	})
}

// --- buildSlackMessage ---

func TestBuildSlackMessage(t *testing.T) {
	t.Run("passing suite has no failures section", func(t *testing.T) {
		r := reportData{
			suiteName: "My Suite",
			succeeded: true,
			startTime: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
			duration:  2 * time.Second,
			stats:     testStats{total: 3, passed: 3},
		}

		msg := buildSlackMessage(r, "https://example.com")

		// header, status, fields, divider, link = 5 blocks
		if len(msg.Blocks) != 5 {
			t.Errorf("blocks = %d, want 5 for passing suite", len(msg.Blocks))
		}
		if !strings.Contains(msg.Blocks[1].Text.Text, "PASSED") {
			t.Errorf("status block should say PASSED, got %q", msg.Blocks[1].Text.Text)
		}
		if strings.Contains(msg.Blocks[1].Text.Text, "FAILED") {
			t.Error("status block should not say FAILED")
		}
	})

	t.Run("failed suite includes failure blocks", func(t *testing.T) {
		r := reportData{
			suiteName: "My Suite",
			succeeded: false,
			startTime: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
			duration:  1 * time.Second,
			stats:     testStats{total: 2, passed: 1, failed: 1},
			failures: []failureDetail{
				{testName: "pkg > test one", errorMsg: "oops"},
			},
		}

		msg := buildSlackMessage(r, "https://example.com")

		// header, status, fields, divider, "Failed Tests:", failure, divider, link = 8 blocks
		if len(msg.Blocks) != 8 {
			t.Errorf("blocks = %d, want 8", len(msg.Blocks))
		}
		if !strings.Contains(msg.Blocks[1].Text.Text, "FAILED") {
			t.Errorf("status block should say FAILED, got %q", msg.Blocks[1].Text.Text)
		}
		if !strings.Contains(msg.Blocks[5].Text.Text, "pkg > test one") {
			t.Errorf("failure block missing test name, got %q", msg.Blocks[5].Text.Text)
		}
	})

	t.Run("more than 5 failures shows overflow line", func(t *testing.T) {
		failures := make([]failureDetail, 7)
		for i := range failures {
			failures[i] = failureDetail{testName: "t", errorMsg: "e"}
		}

		r := reportData{
			succeeded: false,
			startTime: time.Now(),
			failures:  failures,
			stats:     testStats{total: 7, failed: 7},
		}

		msg := buildSlackMessage(r, "https://example.com")

		// header, status, fields, divider, "Failed Tests:", 5×failure, overflow, divider, link = 13
		if len(msg.Blocks) != 13 {
			t.Errorf("blocks = %d, want 13", len(msg.Blocks))
		}
		overflowBlock := msg.Blocks[10]
		if !strings.Contains(overflowBlock.Text.Text, "2 more") {
			t.Errorf("overflow block = %q, want '2 more'", overflowBlock.Text.Text)
		}
	})
}

// --- formatDuration ---

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{90 * time.Second, "1.5m"},
	}

	for _, c := range cases {
		got := formatDuration(c.d)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
