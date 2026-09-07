package output

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type artifactReaderFunc func([]byte) (int, error)

func (f artifactReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestArtifactRejectsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	path, target := filepath.Join(dir, "report"), filepath.Join(t.TempDir(), "missing")
	require.NoError(t, os.Symlink(target, path))
	_, err := SaveArtifact(path, strings.NewReader("downloaded"))
	require.Error(t, err)
	_, err = os.Lstat(target)
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestArtifactRejectsDestinationChangedDuringTransfer(t *testing.T) {
	dir := t.TempDir()
	path, target := filepath.Join(dir, "report"), filepath.Join(t.TempDir(), "sentinel")
	require.NoError(t, os.WriteFile(target, []byte("preserve"), 0600))
	body := artifactReaderFunc(func(p []byte) (int, error) {
		require.NoError(t, os.Symlink(target, path))
		return copy(p, "downloaded"), io.EOF
	})
	_, err := SaveArtifact(path, body)
	require.Error(t, err)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestArtifactRemainsInOpenedDirectoryAfterParentRename(t *testing.T) {
	parent := t.TempDir()
	dir, moved, other := filepath.Join(parent, "output"), filepath.Join(parent, "moved"), t.TempDir()
	require.NoError(t, os.Mkdir(dir, 0700))
	path := filepath.Join(dir, "report")
	require.NoError(t, os.WriteFile(filepath.Join(other, "report"), []byte("preserve"), 0600))
	body := artifactReaderFunc(func(p []byte) (int, error) {
		require.NoError(t, os.Rename(dir, moved))
		require.NoError(t, os.Symlink(other, dir))
		return copy(p, "downloaded"), io.EOF
	})
	_, err := SaveArtifact(path, body)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(other, "report"))
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	data, err = os.ReadFile(filepath.Join(moved, "report"))
	require.NoError(t, err)
	require.Equal(t, "downloaded", string(data))
	entries, err := os.ReadDir(moved)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestArtifactFailureToRenamePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	defer os.Chmod(dir, 0700)
	path := filepath.Join(dir, "report")
	require.NoError(t, os.WriteFile(path, []byte("preserve"), 0600))
	body := artifactReaderFunc(func(p []byte) (int, error) {
		require.NoError(t, os.Chmod(dir, 0500))
		return copy(p, "downloaded"), io.EOF
	})
	_, err := SaveArtifact(path, body)
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	// The directory lost write permission: cleanup also cannot remove the temp.
	// Restoring permissions here lets the test's temporary-directory cleanup run.
}
