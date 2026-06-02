package main

import "time"

type TestStatus string

const (
	StatusPassed  TestStatus = "passed"
	StatusFailed  TestStatus = "failed"
	StatusSkipped TestStatus = "skipped"
	StatusOther   TestStatus = "other"
)

type TestRun struct {
	Name      string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Tests     []TestCase
}

type TestCase struct {
	ID        string
	Suite     string
	Name      string
	File      string
	Line      int
	Status    TestStatus
	RawState  string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Message   string
	Output    string
}

type Stats struct {
	Total   int
	Passed  int
	Failed  int
	Skipped int
	Other   int
}

type Analysis struct {
	Current     TestRun
	Previous    *TestRun
	Stats       Stats
	Failures    []TestCase
	Skipped     []TestCase
	Compare     *Comparison
	GrafanaLogs *GrafanaLogEnrichment
}

type Comparison struct {
	NewFailures       []TestCase
	RecurringFailures []TestCase
	ResolvedFailures  []TestCase
	NewSkips          []TestCase
	RecurringSkips    []TestCase
	ResolvedSkips     []TestCase
	PassedDelta       int
	FailedDelta       int
	SkippedDelta      int
	DurationDelta     time.Duration
}

type GrafanaLogEnrichment struct {
	DatasourceUID  string
	DatasourceName string
	StartRFC3339   string
	EndRFC3339     string
	Contexts       []GrafanaLogContext
}

type GrafanaLogContext struct {
	Test              *TestCase
	FailureRef        string
	TestName          string
	BackendArea       string
	ExpectedError     string
	SearchTerms       []string
	Confidence        string
	Query             string
	GrafanaExploreURL string
	Entries           []GrafanaLogEntry
	RawLineCount      int
	LineCount         int
	FilteredLineCount int
	Truncated         bool
	Error             string
	QueryLabel        string
	Reason            string
}

type GrafanaLogEntry struct {
	Timestamp          string
	Line               string
	Labels             map[string]string
	StructuredMetadata map[string]string
	Parsed             map[string]string
}
