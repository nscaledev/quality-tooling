package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	formatAuto           = "auto"
	formatGinkgoJSON     = "ginkgo-json"
	formatJUnit          = "junit"
	formatPlaywrightJSON = "playwright-json"
)

func parseTestResults(data []byte, format string) (TestRun, error) {
	normalized := normalizeFormat(format)
	if normalized == formatAuto {
		detected, err := detectFormat(data)
		if err != nil {
			return TestRun{}, err
		}
		normalized = detected
	}

	switch normalized {
	case formatGinkgoJSON:
		return parseGinkgoJSON(data)
	case formatJUnit:
		return parseJUnit(data)
	case formatPlaywrightJSON:
		return parsePlaywrightJSON(data)
	default:
		return TestRun{}, fmt.Errorf("unsupported test result format %q", format)
	}
}

func detectFormat(data []byte) (string, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", fmt.Errorf("cannot detect empty test results")
	}

	if strings.HasPrefix(trimmed, "<") {
		if strings.Contains(trimmed, "<testsuite") || strings.Contains(trimmed, "<testsuites") {
			return formatJUnit, nil
		}
		return "", fmt.Errorf("cannot detect XML test result format")
	}

	if strings.HasPrefix(trimmed, "[") {
		return formatGinkgoJSON, nil
	}

	if strings.HasPrefix(trimmed, "{") {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return "", fmt.Errorf("cannot detect JSON test result format: %w", err)
		}
		if _, ok := object["suites"]; ok {
			return formatPlaywrightJSON, nil
		}
		if _, ok := object["SpecReports"]; ok {
			return formatGinkgoJSON, nil
		}
	}

	return "", fmt.Errorf("cannot detect test result format")
}

func normalizeFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "auto":
		return formatAuto
	case "ginkgo", "ginkgo-json", "ginkgo-v2-json":
		return formatGinkgoJSON
	case "junit", "junit-xml", "xml":
		return formatJUnit
	case "playwright", "playwright-json", "pw-json":
		return formatPlaywrightJSON
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}
