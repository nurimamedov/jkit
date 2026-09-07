//go:build unix

package config

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Open a private store and validate the opened directory, not just its pathname.
func openStore(dir string) (*os.Root, error) {
	dir = filepath.Clean(dir)
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("config directory must be a real directory, not a symlink")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil {
		err = checkPrivate(opened, 0700)
	}
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("config directory changed while opening")
	}
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("unsafe config directory: %w", err)
	}
	return root, nil
}

func checkPrivate(info os.FileInfo, mode os.FileMode) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) {
		return errors.New("must be owned by the current user")
	}
	if info.Mode().Perm() != mode || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("permissions must be %04o", mode)
	}
	return nil
}

func openCredentials(root *os.Root) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	// Root.OpenFile resolves contained symlinks even with O_NOFOLLOW. Use openat
	// against its directory descriptor to reject the link atomically instead.
	// NONBLOCK avoids hanging if the file was replaced with a FIFO.
	fd, err := unix.Openat(int(dir.Fd()), "config.yml", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: "config.yml", Err: err}
	}
	f := os.NewFile(uintptr(fd), "config.yml")
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("config file must be a regular file")
	}
	if err == nil {
		err = checkPrivate(info, 0600)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe config file: %w", err)
	}
	return f, nil
}

func readCredentials(dir string) ([]byte, error) {
	root, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := openCredentials(root)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func writeCredentials(dir string, data []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	root, err := openStore(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := checkExistingCredentials(root); err != nil {
		return err
	}
	return writeStoreFile(root, "config.yml", data)
}

func writeStoreFile(root *os.Root, target string, data []byte) error {
	name := ".config-" + rand.Text()
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("creating temporary config: %w", err)
	}
	defer root.Remove(name)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing config: %w", err)
	}
	if err := checkExistingCredentials(root); err != nil {
		return err
	}
	if err := root.Rename(name, target); err != nil {
		return fmt.Errorf("replacing config: %w", err)
	}
	return nil
}

func checkExistingCredentials(root *os.Root) error {
	f, err := openCredentials(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func replaceCredentialsWithBackup(dir string, expected, data []byte) error {
	root, err := openStore(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := openCredentials(root)
	if err != nil {
		return err
	}
	current, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return errors.New("config changed while hosts were checked — re-run")
	}
	if err := writeStoreFile(root, "config.yml.bak", current); err != nil {
		return err
	}
	return writeStoreFile(root, "config.yml", data)
}
