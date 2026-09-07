package output

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SaveArtifact replaces a regular destination only after the complete body is
// written and closed. The directory handle anchors all operations across renames.
func SaveArtifact(path string, body io.Reader) (int64, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return 0, fmt.Errorf("opening output directory: %w", err)
	}
	defer root.Close()
	name := filepath.Base(path)
	if err := checkArtifactDestination(root, name); err != nil {
		return 0, err
	}
	tmp := ".jkit-artifact-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, fmt.Errorf("creating temporary artifact: %w", err)
	}
	defer root.Remove(tmp)
	defer f.Close()
	n, err := io.Copy(f, body)
	if err != nil {
		return 0, fmt.Errorf("writing artifact: %w", err)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("syncing artifact: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("closing artifact: %w", err)
	}
	if err := checkArtifactDestination(root, name); err != nil {
		return 0, err
	}
	// Rename replaces the directory entry itself; it never follows a final symlink.
	if err := root.Rename(tmp, name); err != nil {
		return 0, fmt.Errorf("replacing artifact: %w", err)
	}
	return n, nil
}

func checkArtifactDestination(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking artifact destination: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("artifact destination must be a regular file, not a symlink or directory")
	}
	return nil
}
