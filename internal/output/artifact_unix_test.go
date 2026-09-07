//go:build unix

package output

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactWriteFailurePreservesDestination(t *testing.T) {
	if os.Getenv("JKIT_TEST_ARTIFACT_WRITE_FAILURE") == "1" {
		dir := t.TempDir()
		path := filepath.Join(dir, "report")
		require.NoError(t, os.WriteFile(path, []byte("preserve"), 0600))
		var limit syscall.Rlimit
		require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit))
		limit.Cur = 0
		signal.Ignore(syscall.SIGXFSZ)
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit))
		_, err := SaveArtifact(path, strings.NewReader("downloaded"))
		require.ErrorContains(t, err, "writing artifact")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "preserve", string(data))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(binary, "-test.run=^TestArtifactWriteFailurePreservesDestination$")
	cmd.Env = []string{"JKIT_TEST_ARTIFACT_WRITE_FAILURE=1"}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
