package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeJobConfig(t *testing.T, input []byte, args ...string) (string, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "input")
	require.NoError(t, err)
	defer file.Close()
	_, err = file.Write(input)
	require.NoError(t, err)
	_, err = file.Seek(0, 0)
	require.NoError(t, err)
	previous := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = previous }()
	for _, command := range rootCmd.Commands() {
		if command.Name() == "job-config" {
			for _, child := range command.Commands() {
				child.Flags().VisitAll(func(flag *pflag.Flag) { require.NoError(t, flag.Value.Set(flag.DefValue)); flag.Changed = false })
			}
		}
	}
	return executeCmd(t, append([]string{"job-config"}, args...)...)
}

func TestJobConfigCommandExactGetAndReceipt(t *testing.T) {
	xml := []byte("<?xml version='1.0' encoding='UTF-8'?>\n<project><description>private-secret &amp; text</description></project>\n")
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(404)
			return
		}
		assert.Equal(t, "/job/team/job/demo/config.xml", r.URL.Path)
		if r.Method == "POST" {
			posted, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
			return
		}
		_, _ = w.Write(xml)
	}))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	out, err := executeJobConfig(t, nil, "get", "team/demo", "--stdout")
	require.NoError(t, err)
	assert.Equal(t, string(xml), out)
	out, err = executeJobConfig(t, xml, "update", "team/demo", "--stdin", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"job":"team/demo","submittedSha256":"%x","accepted":true}`, sha256.Sum256(xml)), out)
	assert.Equal(t, xml, posted)
	assert.NotContains(t, out, "private-secret")
}

func TestJobConfigCommandInvalidBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	for _, args := range [][]string{
		{"get", "demo"}, {"get", "demo", "--stdout", "--json"}, {"get", "demo", "--stdout", "--format", "secret"},
		{"update", "demo", "--json"}, {"update", "demo", "--stdin"}, {"update", "../demo", "--stdin", "--json"},
		{"update", "demo", "--stdin", "--json", "--verbose"}, {"update", "demo", "--stdin", "--json", "--branch", "other"},
		{"get", "demo", "--stdout", "--timeout", "0s"}, {"get", "demo", "--stdout", "--timeout", "bad"},
	} {
		out, err := executeJobConfig(t, []byte("<project/>"), args...)
		assert.Error(t, err, args)
		assert.Empty(t, out)
	}
	for _, input := range []string{"", "<project>", "<!DOCTYPE project><project/>", strings.Repeat("x", (1<<20)+1)} {
		out, err := executeJobConfig(t, []byte(input), "update", "demo", "--stdin", "--json")
		assert.Error(t, err)
		assert.Empty(t, out)
	}
	assert.Zero(t, calls.Load())
}

func TestJobConfigCommandErrorsNeverEmitXML(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "<project>private-server-secret</project>")
	}))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	for _, args := range [][]string{{"get", "demo", "--stdout"}, {"update", "demo", "--stdin", "--json"}} {
		out, err := executeJobConfig(t, []byte("<project>private-input-secret</project>"), args...)
		require.Error(t, err)
		assert.Empty(t, out)
		assert.NotContains(t, err.Error(), "secret")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestJobConfigCommandStalledStdinIsBounded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = previous }()
	for _, command := range rootCmd.Commands() {
		if command.Name() == "job-config" {
			for _, child := range command.Commands() {
				child.Flags().VisitAll(func(flag *pflag.Flag) { require.NoError(t, flag.Value.Set(flag.DefValue)); flag.Changed = false })
			}
		}
	}
	start := time.Now()
	out, err := executeCmd(t, "job-config", "update", "demo", "--stdin", "--json", "--timeout", "50ms")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Less(t, time.Since(start), time.Second)
	assert.Zero(t, calls.Load())
}
