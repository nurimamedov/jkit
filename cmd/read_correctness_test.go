package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ysmaoui/jkit/internal/jenkins"
)

func readFixture(t *testing.T, reports map[int]*jenkins.TestReport, stages map[int][]jenkins.Stage, failures map[string]int) {
	t.Helper()
	t.Setenv("JKIT_PIPELINE_SOURCE", "blueocean")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := failures[r.URL.Path]; code != 0 {
			w.WriteHeader(code)
			return
		}
		for _, number := range []int{1, 41, 42} {
			switch r.URL.Path {
			case fmt.Sprintf("/job/demo/%d/api/json", number):
				_ = json.NewEncoder(w).Encode(jenkins.Build{Number: number, Result: "SUCCESS"})
				return
			case fmt.Sprintf("/job/demo/%d/testReport/api/json", number):
				if reports[number] == nil {
					w.WriteHeader(http.StatusNotFound)
				} else {
					_ = json.NewEncoder(w).Encode(reports[number])
				}
				return
			case fmt.Sprintf("/blue/rest/organizations/jenkins/pipelines/demo/runs/%d/nodes/", number):
				if stages[number] == nil {
					w.WriteHeader(http.StatusNotFound)
				} else {
					_ = json.NewEncoder(w).Encode(stages[number])
				}
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	setupTestConfig(t, srv.URL)
}

func testReport(cases ...jenkins.TestCase) *jenkins.TestReport {
	return &jenkins.TestReport{Duration: 9, PassCount: 2, FailCount: 2, SkipCount: 1,
		Suites: []jenkins.TestSuite{{Name: "suite", Cases: cases}}}
}

func TestReadFiltersEveryFormat(t *testing.T) {
	for _, mode := range []string{"text", "json", "template"} {
		t.Run(mode, func(t *testing.T) {
			var cases []jenkins.TestCase
			for _, status := range []string{"PASSED", "FAILED", "SKIPPED", "FIXED", "REGRESSION"} {
				cases = append(cases, jenkins.TestCase{ClassName: "Demo", Name: "case_" + status, Status: status})
			}
			readFixture(t, map[int]*jenkins.TestReport{42: testReport(cases...)}, nil, nil)
			args := []string{"test", "demo", "42", "--failed"}
			if mode == "json" {
				args = append(args, "--json")
			} else if mode == "template" {
				args = append(args, "--format", "{{range .Suites}}{{range .Cases}}{{.Name}} {{end}}{{end}}")
			}
			out, err := executeCmd(t, args...)
			require.NoError(t, err)
			assert.Contains(t, out, "case_FAILED")
			assert.Contains(t, out, "case_REGRESSION")
			for _, excluded := range []string{"PASSED", "SKIPPED", "FIXED"} {
				assert.NotContains(t, out, "case_"+excluded)
			}
			if mode == "json" {
				var report jenkins.TestReport
				require.NoError(t, json.Unmarshal([]byte(out), &report))
				assert.Equal(t, 2, report.PassCount, "totals describe the full report")
				assert.Equal(t, float64(9), report.Duration)
			}
		})
	}
}

func TestReadNewFailuresUsesQualifiedIdentity(t *testing.T) {
	previous := testReport(jenkins.TestCase{ClassName: "A", Name: "same", Status: "FAILED"}, jenkins.TestCase{ClassName: "B", Name: "same", Status: "FIXED"})
	current := testReport(jenkins.TestCase{ClassName: "A", Name: "same", Status: "REGRESSION"}, jenkins.TestCase{ClassName: "B", Name: "same", Status: "FAILED"})
	previous.Suites = append(previous.Suites, jenkins.TestSuite{Name: "other", Cases: []jenkins.TestCase{{ClassName: "B", Name: "same", Status: "FAILED"}}})
	readFixture(t, map[int]*jenkins.TestReport{41: previous, 42: current}, nil, nil)
	out, err := executeCmd(t, "test", "demo", "42", "--new-failures", "--json")
	require.NoError(t, err)
	var result jenkins.TestReport
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Len(t, result.Suites, 1)
	require.Len(t, result.Suites[0].Cases, 1)
	assert.Equal(t, "B", result.Suites[0].Cases[0].ClassName)
}

func TestReadMissingReportIsJSONNull(t *testing.T) {
	readFixture(t, nil, nil, nil)
	out, err := executeCmd(t, "test", "demo", "42", "--json")
	require.NoError(t, err)
	assert.Equal(t, "null", strings.TrimSpace(out))
	out, err = executeCmd(t, "test", "demo", "42", "--format", "{{if .}}present{{else}}absent{{end}}")
	require.NoError(t, err)
	assert.Equal(t, "absent", out)
}

func TestReadNewFailuresRequiresCurrentReport(t *testing.T) {
	readFixture(t, map[int]*jenkins.TestReport{41: testReport()}, nil, nil)
	for _, args := range [][]string{{}, {"--json"}, {"--format", "{{.}}"}} {
		out, err := executeCmd(t, append([]string{"test", "demo", "42", "--new-failures"}, args...)...)
		require.Error(t, err)
		assert.Empty(t, out)
	}
}

func TestReadNewFailuresRequiresBaseline(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			readFixture(t, map[int]*jenkins.TestReport{42: testReport()}, nil, map[string]int{"/job/demo/41/testReport/api/json": code})
			out, err := executeCmd(t, "test", "demo", "42", "--new-failures", "--json")
			require.Error(t, err)
			assert.Empty(t, out)
			assert.Contains(t, err.Error(), "#41")
		})
	}
}

func TestReadNewFailuresRejectsAmbiguousBaseline(t *testing.T) {
	row := jenkins.TestCase{ClassName: "A", Name: "same", Status: "FAILED"}
	readFixture(t, map[int]*jenkins.TestReport{41: testReport(row, row), 42: testReport(row)}, nil, nil)
	out, err := executeCmd(t, "test", "demo", "42", "--new-failures", "--json")
	require.ErrorContains(t, err, "ambiguous")
	assert.Empty(t, out)
}

func TestReadDiffRejectsUnavailableData(t *testing.T) {
	for _, path := range []string{"/job/demo/41/testReport/api/json", "/job/demo/42/testReport/api/json", "/blue/rest/organizations/jenkins/pipelines/demo/runs/41/nodes/", "/blue/rest/organizations/jenkins/pipelines/demo/runs/42/nodes/"} {
		for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
			t.Run(fmt.Sprintf("%s/%d", path, code), func(t *testing.T) {
				readFixture(t, map[int]*jenkins.TestReport{41: testReport(), 42: testReport()}, map[int][]jenkins.Stage{41: {}, 42: {}}, map[string]int{path: code})
				out, err := executeCmd(t, "diff", "demo", "41", "42", "--json")
				require.Error(t, err)
				assert.Empty(t, out)
			})
		}
	}
}

func TestReadDiffIncludesClassRecoveryAndRemoval(t *testing.T) {
	previous := testReport(jenkins.TestCase{ClassName: "A", Name: "same", Status: "FAILED"}, jenkins.TestCase{ClassName: "B", Name: "same", Status: "PASSED"}, jenkins.TestCase{ClassName: "Gone", Name: "removed", Status: "PASSED"})
	current := testReport(jenkins.TestCase{ClassName: "A", Name: "same", Status: "FIXED"}, jenkins.TestCase{ClassName: "B", Name: "same", Status: "FAILED"})
	readFixture(t, map[int]*jenkins.TestReport{41: previous, 42: current}, map[int][]jenkins.Stage{41: {}, 42: {}}, nil)
	out, err := executeCmd(t, "diff", "demo", "41", "42", "--json")
	require.NoError(t, err)
	var result struct {
		Tests []map[string]string `json:"testDiffs"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.ElementsMatch(t, []map[string]string{
		{"suite": "suite", "className": "A", "test": "same", "old": "FAILED", "new": "FIXED"},
		{"suite": "suite", "className": "B", "test": "same", "old": "PASSED", "new": "FAILED"},
		{"suite": "suite", "className": "Gone", "test": "removed", "old": "PASSED", "new": "(removed)"},
	}, result.Tests)
}

func TestReadDiffQualifiesParallelStages(t *testing.T) {
	stages := []jenkins.Stage{{ID: "1", Name: "parallel"}, {ID: "2", Name: "Linux", FirstParent: "1"}, {ID: "3", Name: "Windows", FirstParent: "1"}, {ID: "4", Name: "Test", FirstParent: "2", Status: "SUCCESS"}, {ID: "5", Name: "Test", FirstParent: "3", Status: "FAILURE"}, {ID: "6", Name: "Gone", Status: "SUCCESS"}}
	current := append([]jenkins.Stage(nil), stages[:5]...)
	current[3].Status = "FAILURE"
	readFixture(t, map[int]*jenkins.TestReport{41: testReport(), 42: testReport()}, map[int][]jenkins.Stage{41: stages, 42: current}, nil)
	out, err := executeCmd(t, "diff", "demo", "41", "42", "--json")
	require.NoError(t, err)
	var result struct {
		Stages []map[string]string `json:"stageDiffs"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.ElementsMatch(t, []map[string]string{
		{"name": "Test", "path": "Linux/Test", "old": "SUCCESS", "new": "FAILURE"},
		{"name": "Gone", "path": "Gone", "old": "SUCCESS", "new": "(removed)"},
	}, result.Stages)
}

func TestReadDiffRejectsAmbiguousStages(t *testing.T) {
	stages := []jenkins.Stage{{ID: "1", Name: "Test"}, {ID: "2", Name: "Test"}}
	readFixture(t, map[int]*jenkins.TestReport{41: testReport(), 42: testReport()}, map[int][]jenkins.Stage{41: stages, 42: stages}, nil)
	out, err := executeCmd(t, "diff", "demo", "41", "42", "--json")
	require.ErrorContains(t, err, "ambiguous")
	assert.Empty(t, out)
}

func TestReadDiffRejectsInvalidStageGraph(t *testing.T) {
	for _, stages := range [][]jenkins.Stage{
		{{ID: "1", Name: "A"}, {ID: "1", Name: "B"}},
		{{Name: "A"}},
		{{ID: "1", Name: "A", FirstParent: "2"}, {ID: "2", Name: "B", FirstParent: "1"}},
	} {
		readFixture(t, map[int]*jenkins.TestReport{41: testReport(), 42: testReport()}, map[int][]jenkins.Stage{41: stages, 42: stages}, nil)
		out, err := executeCmd(t, "diff", "demo", "41", "42", "--json")
		require.Error(t, err)
		assert.Empty(t, out)
	}
}
