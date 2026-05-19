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
	currentByID := map[string]TestCase{}
	previousByID := map[string]TestCase{}

	for _, test := range current.Tests {
		currentByID[test.ID] = test
	}
	for _, test := range previous.Tests {
		previousByID[test.ID] = test
	}

	comparison := &Comparison{
		DurationDelta: current.Duration - previous.Duration,
	}

	currentStats := calculateStats(current.Tests)
	previousStats := calculateStats(previous.Tests)
	comparison.PassedDelta = currentStats.Passed - previousStats.Passed
	comparison.FailedDelta = currentStats.Failed - previousStats.Failed
	comparison.SkippedDelta = currentStats.Skipped - previousStats.Skipped

	for _, test := range current.Tests {
		previousTest, existed := previousByID[test.ID]
		switch test.Status {
		case StatusFailed:
			if existed && previousTest.Status == StatusFailed {
				comparison.RecurringFailures = append(comparison.RecurringFailures, test)
			} else {
				comparison.NewFailures = append(comparison.NewFailures, test)
			}
		case StatusSkipped:
			if existed && previousTest.Status == StatusSkipped {
				comparison.RecurringSkips = append(comparison.RecurringSkips, test)
			} else {
				comparison.NewSkips = append(comparison.NewSkips, test)
			}
		}
	}

	for _, test := range previous.Tests {
		currentTest, existsNow := currentByID[test.ID]
		if test.Status == StatusFailed && (!existsNow || currentTest.Status != StatusFailed) {
			comparison.ResolvedFailures = append(comparison.ResolvedFailures, test)
		}
		if test.Status == StatusSkipped && (!existsNow || currentTest.Status != StatusSkipped) {
			comparison.ResolvedSkips = append(comparison.ResolvedSkips, test)
		}
	}

	return comparison
}
