package jenkins

import "fmt"

type testKey struct{ suite, class, name string }

func failedTest(status string) bool { return status == "FAILED" || status == "REGRESSION" }

func testStatuses(report *TestReport) (map[testKey]string, error) {
	states := make(map[testKey]string)
	for _, suite := range report.Suites {
		for _, tc := range suite.Cases {
			key := testKey{suite.Name, tc.ClassName, tc.Name}
			if _, exists := states[key]; exists {
				return nil, fmt.Errorf("ambiguous test identity: %q / %q / %q", key.suite, key.class, key.name)
			}
			states[key] = tc.Status
		}
	}
	return states, nil
}

// FilterTestReport selects cases without changing the full-report counters or
// duration. A baseline selects only failures that were not previously failing.
func FilterTestReport(report *TestReport, failedOnly bool, baseline *TestReport) (*TestReport, error) {
	if !failedOnly && baseline == nil {
		return report, nil
	}
	var previous map[testKey]string
	if baseline != nil {
		var err error
		previous, err = testStatuses(baseline)
		if err != nil {
			return nil, fmt.Errorf("baseline: %w", err)
		}
		if _, err := testStatuses(report); err != nil {
			return nil, fmt.Errorf("current report: %w", err)
		}
	}
	filtered := *report
	filtered.Suites = make([]TestSuite, 0, len(report.Suites))
	for _, suite := range report.Suites {
		selected := TestSuite{Name: suite.Name, Cases: make([]TestCase, 0)}
		for _, tc := range suite.Cases {
			if failedTest(tc.Status) && !failedTest(previous[testKey{suite.Name, tc.ClassName, tc.Name}]) {
				selected.Cases = append(selected.Cases, tc)
			}
		}
		if len(selected.Cases) > 0 {
			filtered.Suites = append(filtered.Suites, selected)
		}
	}
	return &filtered, nil
}

type TestDiff struct {
	Suite     string `json:"suite"`
	ClassName string `json:"className"`
	Test      string `json:"test"`
	Old       string `json:"old"`
	New       string `json:"new"`
}

// CompareTests includes added, removed, recovered, and regressed cases.
func CompareTests(before, after *TestReport) ([]TestDiff, error) {
	old, err := testStatuses(before)
	if err != nil {
		return nil, err
	}
	current, err := testStatuses(after)
	if err != nil {
		return nil, err
	}
	var diffs []TestDiff
	for _, suite := range after.Suites {
		for _, tc := range suite.Cases {
			status, exists := old[testKey{suite.Name, tc.ClassName, tc.Name}]
			if !exists || status != tc.Status {
				diffs = append(diffs, TestDiff{suite.Name, tc.ClassName, tc.Name, status, tc.Status})
			}
		}
	}
	for _, suite := range before.Suites {
		for _, tc := range suite.Cases {
			if _, exists := current[testKey{suite.Name, tc.ClassName, tc.Name}]; !exists {
				diffs = append(diffs, TestDiff{suite.Name, tc.ClassName, tc.Name, tc.Status, "(removed)"})
			}
		}
	}
	return diffs, nil
}
