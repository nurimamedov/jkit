//go:build unix

package config

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

type credentialInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i credentialInfo) Sys() any { return &i.stat }

func TestCredentialOwnershipMustMatchCurrentUser(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	require.NoError(t, err)
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid++
	require.ErrorContains(t, checkPrivate(credentialInfo{info, stat}, info.Mode().Perm()), "current user")
}

func TestCredentialWriteFailurePreservesDestination(t *testing.T) {
	if os.Getenv("JKIT_TEST_CONFIG_WRITE_FAILURE") == "1" {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0700))
		t.Setenv("JKIT_CONFIG_DIR", dir)
		path := filepath.Join(dir, "config.yml")
		require.NoError(t, os.WriteFile(path, []byte("hosts: {}\n"), 0600))
		var limit syscall.Rlimit
		require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit))
		limit.Cur = 0
		signal.Ignore(syscall.SIGXFSZ)
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit))
		cfg := &Config{Hosts: map[string]*HostConfig{"https://example.invalid": {Token: "dummy"}}}
		require.ErrorContains(t, cfg.Save(), "writing config")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "hosts: {}\n", string(data))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		return
	}
	// Restrict only a child process, so other tests retain normal filesystem limits.
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(binary, "-test.run=^TestCredentialWriteFailurePreservesDestination$")
	cmd.Env = []string{"JKIT_TEST_CONFIG_WRITE_FAILURE=1"}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
