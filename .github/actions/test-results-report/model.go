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
	HistoryLogs *TestHistoryLogEnrichment
	UnikornCRs  *UnikornCREnrichment
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

type TestHistoryLogEnrichment struct {
	DatasourceUID  string
	DatasourceName string
	StartRFC3339   string
	EndRFC3339     string
	Contexts       []TestHistoryLogContext
}

type TestHistoryLogContext struct {
	Test               *TestCase
	TestName           string
	TestID             string
	FailureFingerprint string
	Query              string
	SearchTerm         string
	Reason             string
	RawLineCount       int
	LineCount          int
	FilteredLineCount  int
	Truncated          bool
	Error              string
	Observations       []TestHistoryLogObservation
}

type TestHistoryLogObservation struct {
	Timestamp          string
	Repo               string
	Suite              string
	Env                string
	RunID              string
	RunAttempt         string
	TestID             string
	TestName           string
	FailureCategory    string
	FailureFingerprint string
	AILikelyReason     string
	AINextCheck        string
	AIMatchStrategy    string
	ArtifactURL        string
}

type UnikornCREnrichment struct {
	Contexts []UnikornCRContext `json:"contexts"`
}

type UnikornCRContext struct {
	Test        *TestCase                `json:"-"`
	FailureRef  string                   `json:"failure_ref,omitempty"`
	TestName    string                   `json:"test_name,omitempty"`
	BackendArea string                   `json:"backend_area,omitempty"`
	Resource    string                   `json:"resource,omitempty"`
	Namespace   string                   `json:"namespace,omitempty"`
	Name        string                   `json:"name,omitempty"`
	Selector    string                   `json:"selector,omitempty"`
	Reason      string                   `json:"reason,omitempty"`
	Confidence  string                   `json:"confidence,omitempty"`
	ResultCount int                      `json:"result_count"`
	Objects     []UnikornCRObjectSummary `json:"objects,omitempty"`
	Error       string                   `json:"error,omitempty"`
}

type UnikornCRObjectSummary struct {
	APIVersion        string   `json:"api_version,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	Name              string   `json:"name,omitempty"`
	Phase             string   `json:"phase,omitempty"`
	State             string   `json:"state,omitempty"`
	ProvisioningState string   `json:"provisioning_state,omitempty"`
	Health            string   `json:"health,omitempty"`
	DeletionTimestamp string   `json:"deletion_timestamp,omitempty"`
	Conditions        []string `json:"conditions,omitempty"`
	OwnerRefs         []string `json:"owner_refs,omitempty"`
}
