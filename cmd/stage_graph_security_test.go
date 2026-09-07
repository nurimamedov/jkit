package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ysmaoui/jkit/internal/jenkins"
)

func TestReadCommandsRejectMalformedStageGraph(t *testing.T) {
	for name, graph := range map[string][]jenkins.Stage{
		"self cycle":         {{ID: "1", Name: "Build", FirstParent: "1"}},
		"disconnected cycle": {{ID: "root", Name: "Setup"}, {ID: "1", Name: "Build", FirstParent: "2"}, {ID: "2", Name: "Test", FirstParent: "1"}},
		"duplicate ID":       {{ID: "1", Name: "Build"}, {ID: "1", Name: "Test"}},
		"missing ID":         {{Name: "Build"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("JKIT_PIPELINE_SOURCE", "blueocean")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/testReport/api/json"):
					_, _ = w.Write([]byte(`{"suites":[]}`))
				case strings.HasSuffix(r.URL.Path, "/api/json"):
					_, _ = w.Write([]byte(`{"number":42,"result":"FAILURE"}`))
				case strings.HasSuffix(r.URL.Path, "/nodes/"):
					_ = json.NewEncoder(w).Encode(graph)
				default:
					t.Errorf("malformed graph caused unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			setupTestConfig(t, srv.URL)
			for _, args := range [][]string{
				{"stages", "demo", "42"},
				{"stages", "demo", "42", "--json"},
				{"stages", "demo", "42", "--format", "{{len .}}"},
				{"log", "demo", "42", "--stage", "Build"},
				{"diff", "demo", "41", "42", "--json"},
				{"diagnose", "demo", "42", "--json"},
				{"status", "demo", "42"},
			} {
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					out, err := executeCmd(t, args...)
					require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph)
					require.Empty(t, out, "a malformed graph must not produce partial success output")
				})
			}
		})
	}
}

func TestResolveStageIDRejectsMalformedGraph(t *testing.T) {
	id, err := jenkins.ResolveStageID([]jenkins.Stage{{ID: "1", Name: "Build", FirstParent: "1"}}, "Build")
	require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph)
	require.Empty(t, id)
}

func TestReadCommandsRejectStageExpansionBeforeOutput(t *testing.T) {
	for _, test := range []struct {
		name               string
		levels, nameLength int
		args               []string
	}{
		{"qualified paths", 130, 256, []string{"stages", "demo", "42", "--json"}},
		{"text indentation", 1500, 1, []string{"status", "demo", "42"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stages := []jenkins.Stage{{ID: "root", Name: "Root"}}
			parent := "root"
			for i := 0; i < test.levels; i++ {
				branch := fmt.Sprintf("branch-%d", i)
				stages = append(stages,
					jenkins.Stage{ID: branch, Name: strings.Repeat("X", test.nameLength), FirstParent: parent},
					jenkins.Stage{ID: fmt.Sprintf("sibling-%d", i), Name: "Y", FirstParent: parent})
				parent = branch
			}
			readFixture(t, nil, map[int][]jenkins.Stage{42: stages}, nil)
			out, err := executeCmd(t, test.args...)
			require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph)
			require.Empty(t, out)
		})
	}
}

func TestStagesRejectsTextColumnExpansion(t *testing.T) {
	for _, field := range []string{"id", "status", "type", "combined"} {
		t.Run(field, func(t *testing.T) {
			stages := make([]jenkins.Stage, 9)
			for i := range stages {
				stages[i] = jenkins.Stage{ID: fmt.Sprint(i), Name: "Build", Type: "STAGE", Status: "SUCCESS"}
			}
			switch field {
			case "id":
				stages[0].ID = strings.Repeat("I", 1<<20)
			case "status":
				stages[0].Status = strings.Repeat("S", 1<<20)
			case "type":
				stages[0].Type = strings.Repeat("T", 1<<20)
			case "combined":
				stages[0].ID = strings.Repeat("I", 1<<19)
				stages[1].Status = strings.Repeat("S", 1<<19)
			}
			readFixture(t, nil, map[int][]jenkins.Stage{42: stages}, nil)
			out, err := executeCmd(t, "stages", "demo", "42")
			require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph, "remote text columns must not expand into an excessive padded table")
			require.Empty(t, out)
			// JSON carries these scalars once; the table-only budget must not
			// reinterpret an ordinary scalar response as a graph failure.
			jsonOut, jsonErr := executeCmd(t, "stages", "demo", "42", "--json")
			require.NoError(t, jsonErr)
			var got []stageInfo
			require.NoError(t, json.Unmarshal([]byte(jsonOut), &got))
			require.Len(t, got, len(stages))
			require.Equal(t, stages[0].ID, got[0].ID)
			require.Equal(t, stages[0].Status, got[0].Status)
		})
	}
}

func TestStagesAllowsBoundedTextColumns(t *testing.T) {
	stages := []jenkins.Stage{
		{ID: strings.Repeat("I", 1<<17), Name: "Build", Type: "STAGE", Status: "SUCCESS"},
		{ID: "2", Name: "Test", Type: "STAGE", Status: strings.Repeat("S", 1<<17)},
	}
	readFixture(t, nil, map[int][]jenkins.Stage{42: stages}, nil)
	out, err := executeCmd(t, "stages", "demo", "42")
	require.NoError(t, err)
	require.True(t, strings.Contains(out, "Build"))
	require.True(t, strings.Contains(out, "Test"))
	require.Less(t, len(out), jenkins.MaxStageGraphExpansionBytes)
}
