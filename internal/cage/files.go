package cage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func atomicWriteFile(path string, data []byte) error {
	return atomicWriteFileMode(path, data, 0o600)
}

func atomicWriteFileMode(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func writeSecretFile(path string, data []byte) error {
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("write %s with mode 0600: %w", path, err)
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func ensurePrivateFile(path string, label string) error {
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("stat %s %s: %w", label, path, err)
	}
	return ensurePrivateInfo(path, label, info, false, 0o600)
}

func ensurePrivateDir(path string, label string) error {
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("stat %s %s: %w", label, path, err)
	}
	return ensurePrivateInfo(path, label, info, true, 0o700)
}

func ensurePrivateDirIfExists(path string, label string) error {
	info, err := os.Lstat(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s %s: %w", label, path, err)
	}
	return ensurePrivateInfo(path, label, info, true, 0o700)
}

func ensurePrivateInfo(path string, label string, info os.FileInfo, wantDir bool, privateMode os.FileMode) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %s must not be a symlink", label, path)
	}

	if wantDir {
		if !info.IsDir() {
			return fmt.Errorf("%s %s is not a directory", label, path)
		}
	} else {
		if info.IsDir() {
			return fmt.Errorf("%s %s is a directory", label, path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s %s is not a regular file", label, path)
		}
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s %s is accessible by group or others; set mode %04o", label, path, privateMode)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s %s owner could not be determined", label, path)
	}
	if ownerUID, currentUID := int(stat.Uid), os.Getuid(); ownerUID != currentUID {
		return fmt.Errorf("%s %s is owned by uid %d, want uid %d", label, path, ownerUID, currentUID)
	}
	return nil
}
