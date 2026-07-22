//go:build linux

package observability

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type spoolDirectory struct {
	fd   int
	stat unix.Stat_t
	name string
}

func openSpoolDirectory(path string) (spoolDirectory, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) || filepath.Base(cleaned) == "." || filepath.Base(cleaned) == string(filepath.Separator) {
		return spoolDirectory{}, errors.New("invalid_spool_path")
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(cleaned), string(filepath.Separator)), string(filepath.Separator))
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return spoolDirectory{}, err
	}
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(fd)
			return spoolDirectory{}, errors.New("unsafe_spool_directory_component")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if err != nil {
			return spoolDirectory{}, err
		}
		fd = next
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil {
			_ = unix.Close(fd)
			return spoolDirectory{}, errors.New("spool_directory_stat_failure")
		}
		worldWritableStickyRoot := stat.Uid == 0 && stat.Mode&0o022 != 0 && stat.Mode&unix.S_ISVTX != 0
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 && !worldWritableStickyRoot || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
			_ = unix.Close(fd)
			return spoolDirectory{}, errors.New("unsafe_spool_directory")
		}
		if index == len(parts)-1 && (stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o777 != 0o700) {
			_ = unix.Close(fd)
			return spoolDirectory{}, errors.New("unsafe_spool_leaf_directory")
		}
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil {
		_ = unix.Close(fd)
		return spoolDirectory{}, errors.New("spool_directory_stat_failure")
	}
	return spoolDirectory{fd: fd, stat: stat, name: filepath.Base(cleaned)}, nil
}

func validateSpoolStat(fd int) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return unix.Stat_t{}, errors.New("unsafe_spool_inode")
	}
	return stat, nil
}

func verifySpoolBinding(directoryFD int, name string, expected unix.Stat_t) error {
	fd, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	actual, err := validateSpoolStat(fd)
	if err != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return errors.New("spool_inode_changed")
	}
	return nil
}

func validateSpoolPath(path string) error {
	directory, err := openSpoolDirectory(path)
	if err != nil {
		return err
	}
	defer unix.Close(directory.fd)
	fd, err := unix.Openat(directory.fd, directory.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	stat, err := validateSpoolStat(fd)
	if err != nil {
		return err
	}
	return verifySpoolBinding(directory.fd, directory.name, stat)
}

func appendSecureSpool(path string, encoded []byte, maximum int64) error {
	directory, err := openSpoolDirectory(path)
	if err != nil {
		return errors.New("spool_directory_failure")
	}
	defer unix.Close(directory.fd)
	fd, err := unix.Openat(directory.fd, directory.name, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("spool_open_failure")
	}
	defer unix.Close(fd)
	stat, err := validateSpoolStat(fd)
	if err != nil {
		return errors.New("spool_permissions_invalid")
	}
	if stat.Size > maximum || int64(len(encoded)) > maximum-stat.Size {
		return ErrBackpressure
	}
	remaining := encoded
	for len(remaining) > 0 {
		written, err := unix.Write(fd, remaining)
		if err != nil || written <= 0 {
			return errors.New("spool_write_failure")
		}
		remaining = remaining[written:]
	}
	if unix.Fsync(fd) != nil || verifySpoolBinding(directory.fd, directory.name, stat) != nil || unix.Fsync(directory.fd) != nil {
		return errors.New("spool_sync_or_binding_failure")
	}
	return nil
}

func readSecureSpool(path string, maximum int64) ([]byte, error) {
	directory, err := openSpoolDirectory(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(directory.fd)
	fd, err := unix.Openat(directory.fd, directory.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "spool")
	defer file.Close()
	stat, err := validateSpoolStat(fd)
	if err != nil || stat.Size > maximum || verifySpoolBinding(directory.fd, directory.name, stat) != nil {
		return nil, errors.New("unsafe_spool_file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("spool_read_failure")
	}
	return data, nil
}

func drainSecureSpool(path string) error {
	directory, err := openSpoolDirectory(path)
	if err != nil {
		return err
	}
	defer unix.Close(directory.fd)
	temporary := directory.name + ".drained"
	fd, err := unix.Openat(directory.fd, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	stat, statErr := validateSpoolStat(fd)
	syncErr := unix.Fsync(fd)
	closeErr := unix.Close(fd)
	if statErr != nil || syncErr != nil || closeErr != nil || verifySpoolBinding(directory.fd, temporary, stat) != nil {
		return errors.New("spool_drain_temp_failure")
	}
	if unix.Renameat(directory.fd, temporary, directory.fd, directory.name) != nil || unix.Fsync(directory.fd) != nil {
		return errors.New("spool_drain_rename_failure")
	}
	return validateSpoolPath(path)
}
