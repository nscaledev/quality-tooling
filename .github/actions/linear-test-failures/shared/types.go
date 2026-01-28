package shared

import "time"

// GinkgoReport represents the JSON output from Ginkgo test runs.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type GinkgoReport struct {
	SuitePath        string       `json:"SuitePath"`
	SuiteDescription string       `json:"SuiteDescription"`
	SuiteSucceeded   bool         `json:"SuiteSucceeded"`
	PreRunStats      PreRunStats  `json:"PreRunStats"`
	StartTime        time.Time    `json:"StartTime"`
	EndTime          time.Time    `json:"EndTime"`
	RunTime          int64        `json:"RunTime"`
	SpecReports      []SpecReport `json:"SpecReports"`
}

// PreRunStats contains test statistics before execution.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type PreRunStats struct {
	TotalSpecs       int `json:"TotalSpecs"`
	SpecsThatWillRun int `json:"SpecsThatWillRun"`
}

// SpecReport contains the results of a single test spec.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type SpecReport struct {
	ContainerHierarchyTexts    []string     `json:"ContainerHierarchyTexts"`
	LeafNodeText               string       `json:"LeafNodeText"`
	State                      string       `json:"State"` // passed, failed, skipped, etc.
	RunTime                    int64        `json:"RunTime"`
	Failure                    *SpecFailure `json:"Failure,omitempty"`
	CapturedGinkgoWriterOutput string       `json:"CapturedGinkgoWriterOutput"`
}

// SpecFailure contains failure details for a test spec.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type SpecFailure struct {
	Message  string   `json:"Message"`
	Location Location `json:"Location"`
}

// Location represents a file location.
//
//nolint:tagliatelle // JSON tags match Ginkgo's output format
type Location struct {
	FileName   string `json:"FileName"`
	LineNumber int    `json:"LineNumber"`
}
