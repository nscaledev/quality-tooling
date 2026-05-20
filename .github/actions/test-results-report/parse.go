package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func parseGinkgoJSON(data []byte) (TestRun, error) {
	var reports []ginkgoReport
	if err := json.Unmarshal(data, &reports); err != nil {
		return TestRun{}, fmt.Errorf("parse ginkgo json: %w", err)
	}
	if len(reports) == 0 {
		return TestRun{}, fmt.Errorf("parse ginkgo json: no suite reports found")
	}

	run := TestRun{Name: "Ginkgo Test Results"}
	if len(reports) == 1 && reports[0].SuiteDescription != "" {
		run.Name = reports[0].SuiteDescription
	}

	for _, report := range reports {
		run.Duration += time.Duration(report.RunTime)
		for _, spec := range report.SpecReports {
			testName := buildGinkgoTestName(spec)
			test := TestCase{
				ID:       testName,
				Suite:    firstNonEmpty(firstString(spec.ContainerHierarchyTexts), report.SuiteDescription),
				Name:     testName,
				Status:   normalizeStatus(spec.State),
				RawState: spec.State,
				Duration: time.Duration(spec.RunTime),
				Output:   strings.TrimSpace(spec.CapturedGinkgoWriterOutput),
			}
			if spec.Failure != nil {
				test.Message = strings.TrimSpace(spec.Failure.Message)
				test.File = spec.Failure.Location.FileName
				test.Line = spec.Failure.Location.LineNumber
			}
			run.Tests = append(run.Tests, test)
		}
	}

	return run, nil
}

func parseJUnit(data []byte) (TestRun, error) {
	var root junitRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return TestRun{}, fmt.Errorf("parse junit xml: %w", err)
	}

	run := TestRun{Name: firstNonEmpty(root.Name, "JUnit Test Results")}
	if root.Time != "" {
		run.Duration = parseSecondsDuration(root.Time)
	}

	switch root.XMLName.Local {
	case "testsuites":
		for _, suite := range root.Suites {
			collectJUnitSuite(suite, nil, &run.Tests)
		}
	case "testsuite":
		suite := junitSuite{
			Name:   root.Name,
			Time:   root.Time,
			Cases:  root.Cases,
			Suites: root.Suites,
		}
		collectJUnitSuite(suite, nil, &run.Tests)
	default:
		return TestRun{}, fmt.Errorf("parse junit xml: unsupported root element %q", root.XMLName.Local)
	}

	if run.Duration == 0 {
		for _, test := range run.Tests {
			run.Duration += test.Duration
		}
	}

	return run, nil
}

func parsePlaywrightJSON(data []byte) (TestRun, error) {
	var report playwrightReport
	if err := json.Unmarshal(data, &report); err != nil {
		return TestRun{}, fmt.Errorf("parse playwright json: %w", err)
	}

	run := TestRun{
		Name:     firstNonEmpty(report.Config.RootDir, "Playwright Test Results"),
		Duration: time.Duration(report.Stats.Duration * float64(time.Millisecond)),
	}

	for _, suite := range report.Suites {
		collectPlaywrightSuite(suite, nil, &run.Tests)
	}

	if len(run.Tests) == 0 {
		return TestRun{}, fmt.Errorf("parse playwright json: no tests found")
	}

	if run.Duration == 0 {
		for _, test := range run.Tests {
			run.Duration += test.Duration
		}
	}

	return run, nil
}

type ginkgoReport struct {
	SuiteDescription string             `json:"SuiteDescription"`
	RunTime          int64              `json:"RunTime"`
	SpecReports      []ginkgoSpecReport `json:"SpecReports"`
}

type ginkgoSpecReport struct {
	ContainerHierarchyTexts    []string       `json:"ContainerHierarchyTexts"`
	LeafNodeText               string         `json:"LeafNodeText"`
	State                      string         `json:"State"`
	RunTime                    int64          `json:"RunTime"`
	Failure                    *ginkgoFailure `json:"Failure,omitempty"`
	CapturedGinkgoWriterOutput string         `json:"CapturedGinkgoWriterOutput"`
}

type ginkgoFailure struct {
	Message  string         `json:"Message"`
	Location ginkgoLocation `json:"Location"`
}

type ginkgoLocation struct {
	FileName   string `json:"FileName"`
	LineNumber int    `json:"LineNumber"`
}

type junitRoot struct {
	XMLName xml.Name
	Name    string       `xml:"name,attr"`
	Time    string       `xml:"time,attr"`
	Suites  []junitSuite `xml:"testsuite"`
	Cases   []junitCase  `xml:"testcase"`
}

type junitSuite struct {
	Name   string       `xml:"name,attr"`
	Time   string       `xml:"time,attr"`
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	ClassName string         `xml:"classname,attr"`
	Name      string         `xml:"name,attr"`
	Time      string         `xml:"time,attr"`
	File      string         `xml:"file,attr"`
	Line      int            `xml:"line,attr"`
	Failures  []junitFailure `xml:"failure"`
	Errors    []junitFailure `xml:"error"`
	Skipped   *junitSkipped  `xml:"skipped"`
	SystemOut string         `xml:"system-out"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type playwrightReport struct {
	Config struct {
		RootDir string `json:"rootDir"`
	} `json:"config"`
	Stats struct {
		Duration float64 `json:"duration"`
	} `json:"stats"`
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSuite struct {
	Title  string            `json:"title"`
	File   string            `json:"file"`
	Suites []playwrightSuite `json:"suites"`
	Specs  []playwrightSpec  `json:"specs"`
}

type playwrightSpec struct {
	Title string           `json:"title"`
	File  string           `json:"file"`
	Line  int              `json:"line"`
	Tests []playwrightTest `json:"tests"`
}

type playwrightTest struct {
	ProjectName    string             `json:"projectName"`
	Status         string             `json:"status"`
	ExpectedStatus string             `json:"expectedStatus"`
	Results        []playwrightResult `json:"results"`
}

type playwrightResult struct {
	Status   string             `json:"status"`
	Duration int64              `json:"duration"`
	Error    *playwrightError   `json:"error"`
	Errors   []playwrightError  `json:"errors"`
	Stdout   []playwrightOutput `json:"stdout"`
	Stderr   []playwrightOutput `json:"stderr"`
}

type playwrightError struct {
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

type playwrightOutput struct {
	Text string `json:"text"`
}

func buildGinkgoTestName(spec ginkgoSpecReport) string {
	parts := make([]string, 0, len(spec.ContainerHierarchyTexts)+1)
	parts = append(parts, spec.ContainerHierarchyTexts...)
	parts = append(parts, spec.LeafNodeText)
	return strings.Join(nonEmpty(parts), " > ")
}

func collectJUnitSuite(suite junitSuite, parents []string, tests *[]TestCase) {
	path := append(append([]string{}, parents...), suite.Name)
	path = nonEmpty(path)

	for _, child := range suite.Suites {
		collectJUnitSuite(child, path, tests)
	}

	for _, tc := range suite.Cases {
		suiteName := firstNonEmpty(tc.ClassName, strings.Join(path, " > "))
		name := firstNonEmpty(tc.Name, "(unnamed test)")
		message := ""
		status := StatusPassed
		rawState := "passed"

		if len(tc.Failures) > 0 {
			status = StatusFailed
			rawState = "failure"
			message = formatJUnitFailure(tc.Failures[0])
		} else if len(tc.Errors) > 0 {
			status = StatusFailed
			rawState = "error"
			message = formatJUnitFailure(tc.Errors[0])
		} else if tc.Skipped != nil {
			status = StatusSkipped
			rawState = "skipped"
			message = strings.TrimSpace(firstNonEmpty(tc.Skipped.Message, tc.Skipped.Text))
		}

		file := tc.File
		line := tc.Line
		if file == "" || line == 0 {
			if foundFile, foundLine := extractLocation(message); foundFile != "" {
				file = firstNonEmpty(file, foundFile)
				if line == 0 {
					line = foundLine
				}
			}
		}

		*tests = append(*tests, TestCase{
			ID:       stableID(suiteName, name),
			Suite:    suiteName,
			Name:     name,
			File:     file,
			Line:     line,
			Status:   status,
			RawState: rawState,
			Duration: parseSecondsDuration(tc.Time),
			Message:  message,
			Output:   strings.TrimSpace(tc.SystemOut),
		})
	}
}

func collectPlaywrightSuite(suite playwrightSuite, parents []string, tests *[]TestCase) {
	path := append(append([]string{}, parents...), suite.Title)
	path = nonEmpty(path)

	for _, child := range suite.Suites {
		collectPlaywrightSuite(child, path, tests)
	}

	for _, spec := range suite.Specs {
		file := firstNonEmpty(spec.File, suite.File, firstFile(path))
		suiteName := playwrightSuiteName(path, file)

		for _, test := range spec.Tests {
			status := playwrightStatus(test)
			duration := time.Duration(0)
			for _, result := range test.Results {
				duration += time.Duration(result.Duration) * time.Millisecond
			}
			message := playwrightMessage(test)
			output := playwrightOutputText(test)
			project := firstNonEmpty(test.ProjectName, "default")

			*tests = append(*tests, TestCase{
				ID:       stableID(suiteName, spec.Title, project),
				Suite:    suiteName,
				Name:     spec.Title,
				File:     file,
				Line:     spec.Line,
				Status:   status,
				RawState: test.Status,
				Duration: duration,
				Message:  message,
				Output:   output,
			})
		}
	}
}

func playwrightSuiteName(path []string, file string) string {
	path = nonEmpty(path)
	if file == "" {
		return strings.Join(path, " > ")
	}

	parts := []string{file}
	for _, part := range path {
		if part != file {
			parts = append(parts, part)
		}
	}
	return strings.Join(nonEmpty(parts), " > ")
}

func playwrightStatus(test playwrightTest) TestStatus {
	switch test.Status {
	case "expected", "flaky":
		return StatusPassed
	case "skipped":
		return StatusSkipped
	case "unexpected":
		return StatusFailed
	}

	hasResult := false
	for _, result := range test.Results {
		hasResult = true
		switch result.Status {
		case "failed", "timedOut", "interrupted":
			return StatusFailed
		}
	}

	if hasResult {
		last := test.Results[len(test.Results)-1].Status
		if last == "skipped" {
			return StatusSkipped
		}
		if last == "passed" {
			return StatusPassed
		}
	}

	switch test.Status {
	case "expected", "flaky":
		return StatusPassed
	default:
		return StatusOther
	}
}

func playwrightMessage(test playwrightTest) string {
	for i := len(test.Results) - 1; i >= 0; i-- {
		result := test.Results[i]
		if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
			return strings.TrimSpace(result.Error.Message)
		}
		for _, err := range result.Errors {
			if strings.TrimSpace(err.Message) != "" {
				return strings.TrimSpace(err.Message)
			}
		}
	}
	return ""
}

func playwrightOutputText(test playwrightTest) string {
	var parts []string
	for _, result := range test.Results {
		for _, stdout := range result.Stdout {
			parts = append(parts, stdout.Text)
		}
		for _, stderr := range result.Stderr {
			parts = append(parts, stderr.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func formatJUnitFailure(failure junitFailure) string {
	message := strings.TrimSpace(failure.Message)
	text := strings.TrimSpace(failure.Text)
	if message == "" {
		return text
	}
	if text == "" || strings.Contains(text, message) {
		return message
	}
	return message + "\n" + text
}

func parseSecondsDuration(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func normalizeStatus(value string) TestStatus {
	switch strings.ToLower(value) {
	case "passed", "pass", "success", "expected", "flaky":
		return StatusPassed
	case "failed", "failure", "error", "unexpected", "timedout", "timedOut", "interrupted":
		return StatusFailed
	case "skipped", "skip", "pending":
		return StatusSkipped
	default:
		return StatusOther
	}
}

func extractLocation(value string) (string, int) {
	re := regexp.MustCompile(`([A-Za-z0-9_./\\-]+\.(?:go|ts|tsx|js|jsx|py|java)):(\d+)`)
	match := re.FindStringSubmatch(value)
	if len(match) != 3 {
		return "", 0
	}
	line, _ := strconv.Atoi(match[2])
	return match[1], line
}

func firstFile(values []string) string {
	for _, value := range values {
		if strings.Contains(value, ".") && strings.Contains(value, "/") {
			return value
		}
	}
	return ""
}

func firstString(values []string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func stableID(parts ...string) string {
	return strings.Join(nonEmpty(parts), "::")
}
