package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunJSONReceipt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(404)
			return
		}
		calls.Add(1)
		assert.Equal(t, "/job/team/job/demo/buildWithParameters", r.URL.Path)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "main", r.Form.Get("BRANCH"))
		w.Header().Set("Location", "/queue/item/42/")
		w.WriteHeader(201)
	}))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	out, err := executeCmd(t, "run", "team/demo", "-p", "BRANCH=main", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"team/demo","queueId":42,"queueUrl":"`+srv.URL+`/queue/item/42/"}`, out)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRunJSONRejectsWaitAndLogBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	for _, flag := range []string{"--wait", "--log"} {
		out, err := executeCmd(t, "run", "demo", "--json", flag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--json")
		assert.Empty(t, out)
	}
	assert.Zero(t, calls.Load())
}

func TestQueueByIDJSON(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "executable", true: "cancelled"}[cancelled], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/queue/item/42/api/json", r.URL.Path)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "cancelled": cancelled, "task": map[string]any{"name": "demo", "url": "http://jenkins/job/demo/"}, "executable": map[string]any{"number": 7, "url": "http://jenkins/job/demo/7/"}})
			}))
			defer srv.Close()
			setupTestConfig(t, srv.URL)
			out, err := executeCmd(t, "queue", "--id", "42", "--json")
			require.NoError(t, err)
			var item map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &item))
			assert.Equal(t, float64(42), item["id"])
			assert.Equal(t, cancelled, item["cancelled"])
		})
	}
}

func TestQueueByIDRejectsInvalidOptionsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	for _, args := range [][]string{{"--id", "0", "--json"}, {"--id", "-1", "--json"}, {"--id", "42", "--job", "demo", "--json"}} {
		out, err := executeCmd(t, append([]string{"queue"}, args...)...)
		require.Error(t, err)
		assert.Empty(t, out)
	}
	assert.Zero(t, calls.Load())
}

func TestArtifactStdoutBoundedExactDownload(t *testing.T) {
	for _, kind := range []string{"success", "limit", "overflow", "interrupted", "missing", "wrong path", "duplicate", "http error"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var downloads atomic.Int32
			content := "{\"expectedSha\":\"abc\"}\n"
			if kind == "limit" {
				content = strings.Repeat("a", 64<<10)
			}
			if kind == "overflow" {
				content = strings.Repeat("a", (64<<10)+1)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/job/demo/7/api/json" {
					artifacts := []map[string]string{{"fileName": "ci-gate-evidence.json", "relativePath": "ci-gate-evidence.json"}}
					switch kind {
					case "missing":
						artifacts = nil
					case "wrong path":
						artifacts[0]["relativePath"] = "other/ci-gate-evidence.json"
					case "duplicate":
						artifacts = append(artifacts, artifacts[0])
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": artifacts})
					return
				}
				downloads.Add(1)
				assert.Equal(t, "/job/demo/7/artifact/ci-gate-evidence.json", r.URL.Path)
				if kind == "http error" {
					w.WriteHeader(404)
					return
				}
				if kind == "interrupted" {
					w.Header().Set("Content-Length", "1000")
				}
				_, _ = io.WriteString(w, content)
			}))
			defer srv.Close()
			setupTestConfig(t, srv.URL)
			out, err := executeCmd(t, "artifacts", "demo", "7", "--download", "ci-gate-evidence.json", "--stdout")
			if kind == "success" || kind == "limit" {
				require.NoError(t, err)
				assert.Equal(t, content, out)
			} else {
				require.Error(t, err)
				assert.Empty(t, out)
			}
			entries, readErr := os.ReadDir(".")
			require.NoError(t, readErr)
			assert.Empty(t, entries, "stdout mode must never write files")
			if kind == "missing" || kind == "wrong path" || kind == "duplicate" {
				assert.Zero(t, downloads.Load())
			} else {
				assert.Equal(t, int32(1), downloads.Load())
			}
		})
	}
}

func TestArtifactStdoutRejectsInvalidOptionsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	cases := [][]string{{"demo", "7"}, {"demo", "--download", "e.json"}, {"demo", "0", "--download", "e.json"}, {"demo", "-1", "--download", "e.json"}, {"demo", "7", "--download", "e.json", "--output", "x"}, {"demo", "7", "--download", "e.json", "--output="}}
	for _, name := range []string{"../e.json", "/e.json", "a/../e.json", "a//e.json", "%2e%2e/e.json", "a\\e.json", "e.json?x", "e.json#x", "./e.json", "."} {
		cases = append(cases, []string{"demo", "7", "--download", name})
	}
	for _, args := range cases {
		out, err := executeCmd(t, append(append([]string{"artifacts"}, args...), "--stdout")...)
		require.Error(t, err, "%v", args)
		assert.Empty(t, out)
	}
	assert.Zero(t, calls.Load())
}
