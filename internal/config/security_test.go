package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialStoreRejectsUnsafePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlinks")
	}
	for _, kind := range []string{"directory permissions", "file permissions", "directory symlink", "directory symlink trailing slash", "file symlink", "contained file symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "jkit")
			require.NoError(t, os.Mkdir(dir, 0700))
			path := filepath.Join(dir, "config.yml")
			original := []byte("hosts: {}\n")
			require.NoError(t, os.WriteFile(path, original, 0600))
			switch kind {
			case "directory permissions":
				require.NoError(t, os.Chmod(dir, 0755))
			case "file permissions":
				require.NoError(t, os.Chmod(path, 0644))
			case "directory symlink", "directory symlink trailing slash":
				link := filepath.Join(parent, "linked")
				require.NoError(t, os.Symlink(dir, link))
				dir = link
				if kind == "directory symlink trailing slash" {
					dir += string(os.PathSeparator)
				}
			case "file symlink", "contained file symlink":
				target := filepath.Join(parent, "sentinel")
				if kind == "contained file symlink" {
					target = filepath.Join(dir, "sentinel")
				}
				require.NoError(t, os.Rename(path, target))
				if kind == "contained file symlink" {
					target = "sentinel"
				}
				require.NoError(t, os.Symlink(target, path))
			}
			t.Setenv("JKIT_CONFIG_DIR", dir)
			_, err := Load()
			require.Error(t, err, "unsafe store must not be read")
			cfg := &Config{Hosts: map[string]*HostConfig{"https://example.invalid": {Token: "dummy-new-token"}}}
			require.Error(t, cfg.Save(), "unsafe store must not be written")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, data, "refusal must preserve old contents")
		})
	}
}

func TestCredentialSaveReplacesFileAtomically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "jkit")
	t.Setenv("JKIT_CONFIG_DIR", dir)
	cfg := &Config{Hosts: map[string]*HostConfig{"https://example.invalid": {Token: "dummy-old-token"}}}
	require.NoError(t, cfg.Save())
	path := filepath.Join(dir, "config.yml")
	oldFile, err := os.Open(path)
	require.NoError(t, err)
	defer oldFile.Close()
	oldInfo, err := oldFile.Stat()
	require.NoError(t, err)
	cfg.Hosts["https://example.invalid"].Token = "dummy-new-token"
	require.NoError(t, cfg.Save())
	newInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.False(t, os.SameFile(oldInfo, newInfo), "atomic save must replace, not truncate, the old file")
	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, "dummy-new-token", loaded.Hosts["https://example.invalid"].Token)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), newInfo.Mode().Perm())
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "temporary credentials must be cleaned up")
}

func TestCredentialStoreRejectsDanglingFileSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	t.Setenv("JKIT_CONFIG_DIR", dir)
	require.NoError(t, os.Symlink("missing.yml", filepath.Join(dir, "config.yml")))
	_, err := Load()
	require.Error(t, err, "dangling link must not appear to be an unconfigured store")
	require.Error(t, (&Config{Hosts: map[string]*HostConfig{}}).Save())
	_, err = os.Stat(filepath.Join(dir, "missing.yml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
