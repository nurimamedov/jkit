package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ysmaoui/jkit/internal/api"
)

func TestAbortJSONCommand(t *testing.T) {
	posts := 0
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/demo/api/json":
			io.WriteString(w, `{"fullName":"demo"}`)
		case "/job/demo/42/api/json":
			fmt.Fprintf(w, `{"number":42,"building":true,"url":%q}`, host+"/job/demo/42/")
		case "/crumbIssuer/api/json":
			w.WriteHeader(404)
		case "/job/demo/42/stop":
			posts++
			w.Header().Set("Location", host+"/job/demo/42/")
			w.WriteHeader(302)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	host = srv.URL
	setupTestConfig(t, srv.URL)
	out, err := executeCmd(t, "abort", "demo", "42", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"demo","buildNumber":42,"submitted":true,"alreadyCompleted":false}`, out)
	assert.Equal(t, 1, posts)
}

func TestControlCommandsRejectAmbiguityBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	for _, args := range [][]string{
		{"abort", "demo", "--json"}, {"abort", "demo", "0", "--json"}, {"abort", "demo", "-1", "--json"}, {"abort", "demo", "01", "--json"}, {"abort", "demo", "latest", "--json"},
		{"abort", "demo", "42", "--json", "--wait"}, {"abort", "demo", "42", "--json", "--branch", "other"}, {"abort", "demo", "42", "--json", "--verbose"},
		{"job-config", "copy", "source", "dest"}, {"job-config", "copy", "source", "dest", "--json"}, {"job-config", "copy", "source", "dest", "--json", "--expected-sha256", "bad"},
		{"job-config", "copy", "source", "source", "--json", "--expected-sha256", strings.Repeat("0", 64)},
	} {
		out, err := executeCmd(t, args...)
		assert.Error(t, err, args)
		assert.Empty(t, out, args)
	}
	assert.Zero(t, calls)
}

func TestCopyJobJSONCommand(t *testing.T) {
	data := []byte(`<project><triggers/></project>`)
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/source/api/json":
			io.WriteString(w, `{"fullName":"source"}`)
		case "/job/source/config.xml", "/job/dest/config.xml":
			w.Write(data)
		case "/job/dest/api/json":
			if posts == 0 {
				w.WriteHeader(404)
			} else {
				io.WriteString(w, `{"fullName":"dest"}`)
			}
		case "/crumbIssuer/api/json":
			w.WriteHeader(404)
		case "/createItem":
			posts++
			assert.Equal(t, "dest", r.URL.Query().Get("name"))
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	out, err := executeCmd(t, "job-config", "copy", "source", "dest", "--json", "--expected-sha256", hash)
	require.NoError(t, err)
	configuration, err := api.ConfigurationSHA256(data)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"source":"source","destination":"dest","submittedSha256":%q,"configurationSha256":%q,"destinationSha256":%q,"accepted":true,"verified":true}`, hash, configuration, hash), out)
	assert.Equal(t, 1, posts)
}

func TestJobConfigDigestCommand(t *testing.T) {
	data := []byte("<?xml version='1.1'?><project><script> exact text </script></project>")
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		assert.Equal(t, "/job/source/config.xml", r.URL.Path)
		w.Write(data)
	}))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	out, err := executeCmd(t, "job-config", "digest", "source", "--json")
	require.NoError(t, err)
	configuration, err := api.ConfigurationSHA256(data)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"job":"source","sha256":"%x","configurationSha256":%q}`, sha256.Sum256(data), configuration), out)
	assert.Equal(t, 1, reads)
	assert.NotContains(t, out, "exact text")
}
