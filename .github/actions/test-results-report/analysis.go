package main

func calculateStats(tests []TestCase) Stats {
	stats := Stats{Total: len(tests)}
	for _, test := range tests {
		switch test.Status {
		case StatusPassed:
			stats.Passed++
		case StatusFailed:
			stats.Failed++
		case StatusSkipped:
			stats.Skipped++
		default:
			stats.Other++
		}
	}
	return stats
}

func analyze(current TestRun, previous *TestRun) Analysis {
	analysis := Analysis{
		Current: current,
		Stats:   calculateStats(current.Tests),
	}

	for _, test := range current.Tests {
		switch test.Status {
		case StatusFailed:
			analysis.Failures = append(analysis.Failures, test)
		case StatusSkipped:
			analysis.Skipped = append(analysis.Skipped, test)
		}
	}

	if previous != nil {
		analysis.Previous = previous
		analysis.Compare = compareRuns(current, *previous)
	}

	return analysis
}

func compareRuns(current, previous TestRun) *Comparison {
	comparison := &Comparison{
		DurationDelta: current.Duration - previous.Duration,
	}

	currentStats := calculateStats(current.Tests)
	previousStats := calculateStats(previous.Tests)
	comparison.PassedDelta = currentStats.Passed - previousStats.Passed
	comparison.FailedDelta = currentStats.Failed - previousStats.Failed
	comparison.SkippedDelta = currentStats.Skipped - previousStats.Skipped

	currentFailures, currentFailureOrder := firstTestsByStatus(current.Tests, StatusFailed)
	previousFailures, previousFailureOrder := firstTestsByStatus(previous.Tests, StatusFailed)
	currentSkips, currentSkipOrder := firstTestsByStatus(current.Tests, StatusSkipped)
	previousSkips, previousSkipOrder := firstTestsByStatus(previous.Tests, StatusSkipped)

	for _, id := range currentFailureOrder {
		test := currentFailures[id]
		if _, existed := previousFailures[id]; existed {
			comparison.RecurringFailures = append(comparison.RecurringFailures, test)
		} else {
			comparison.NewFailures = append(comparison.NewFailures, test)
		}
	}
	for _, id := range currentSkipOrder {
		test := currentSkips[id]
		if _, existed := previousSkips[id]; existed {
			comparison.RecurringSkips = append(comparison.RecurringSkips, test)
		} else {
			comparison.NewSkips = append(comparison.NewSkips, test)
		}
	}

	for _, id := range previousFailureOrder {
		if _, existsNow := currentFailures[id]; !existsNow {
			comparison.ResolvedFailures = append(comparison.ResolvedFailures, previousFailures[id])
		}
	}
	for _, id := range previousSkipOrder {
		if _, existsNow := currentSkips[id]; !existsNow {
			comparison.ResolvedSkips = append(comparison.ResolvedSkips, previousSkips[id])
		}
	}

	return comparison
}

func firstTestsByStatus(tests []TestCase, status TestStatus) (map[string]TestCase, []string) {
	byID := map[string]TestCase{}
	var order []string
	for _, test := range tests {
		if test.Status != status {
			continue
		}
		if _, exists := byID[test.ID]; exists {
			continue
		}
		byID[test.ID] = test
		order = append(order, test.ID)
	}
	return byID, order
}
