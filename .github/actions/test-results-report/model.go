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
	Name     string
	Duration time.Duration
	Tests    []TestCase
}

type TestCase struct {
	ID       string
	Suite    string
	Name     string
	File     string
	Line     int
	Status   TestStatus
	RawState string
	Duration time.Duration
	Message  string
	Output   string
}

type Stats struct {
	Total   int
	Passed  int
	Failed  int
	Skipped int
	Other   int
}

type Analysis struct {
	Current  TestRun
	Previous *TestRun
	Stats    Stats
	Failures []TestCase
	Skipped  []TestCase
	Compare  *Comparison
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
