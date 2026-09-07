package api

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

// These cases catch malformed remote data being returned as usable stage data.
func TestPipelineRejectsMalformedStageGraph(t *testing.T) {
	cases := map[string][]jenkins.Stage{
		"self cycle":         {{ID: "1", Name: "Build", FirstParent: "1"}},
		"cross cycle":        {{ID: "1", Name: "Build", FirstParent: "2"}, {ID: "2", Name: "Test", FirstParent: "1"}},
		"disconnected cycle": {{ID: "root", Name: "Setup"}, {ID: "1", FirstParent: "2"}, {ID: "2", FirstParent: "1"}},
		"duplicate ID":       {{ID: "1", Name: "Build"}, {ID: "1", Name: "Test"}},
		"missing ID":         {{Name: "Build"}},
	}
	excessive := make([]jenkins.Stage, 10001)
	for i := range excessive {
		excessive[i].ID = fmt.Sprint(i)
	}
	cases["excessive node count"] = excessive
	for name, stages := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewEncoder(w).Encode(stages))
			}))
			defer srv.Close()
			client := NewClient(srv.URL, "dummy", "dummy", WithPipelineSource(PipelineSourceBlueOcean))
			got, err := client.GetPipelineStages("demo", 42)
			require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph, "malformed graph must not be returned successfully")
			require.Nil(t, got)
		})
	}
}

func TestPipelineRejectsDuplicatePGVStageIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","data":{"stages":[{"id":"1","name":"Build","children":[{"id":"1","name":"Test"}]}]}}`))
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, "dummy", "dummy").GetPipelineStages("demo", 42)
	require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph, "PGV duplicate IDs must fail before stage traversal")
	require.Nil(t, got)
}

func TestDiagnoseRejectsMalformedGraphWithoutConsoleFallback(t *testing.T) {
	consoleCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/json"):
			_, _ = w.Write([]byte(`{"number":42,"result":"FAILURE"}`))
		case strings.HasSuffix(r.URL.Path, "/nodes/"):
			_, _ = w.Write([]byte(`[{"id":"1","displayName":"Build","firstParent":"1","result":"FAILURE"}]`))
		default:
			consoleCalls++
			_, _ = w.Write([]byte("error from console"))
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "dummy", "dummy", WithPipelineSource(PipelineSourceBlueOcean))
	got, err := client.Diagnose("demo", 42)
	require.ErrorIs(t, err, jenkins.ErrInvalidStageGraph, "malformed graph must not be hidden by console fallback")
	require.Nil(t, got)
	require.Zero(t, consoleCalls)
}
