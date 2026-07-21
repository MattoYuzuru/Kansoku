//go:build linux

package privacy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const HMACKeyBytes = 32

type secretDirectory struct {
	fd   int
	stat syscall.Stat_t
	name string
}

func CreateHMACKeyFile(path string, random io.Reader) error {
	directory, err := openSecretDirectory(path)
	if err != nil {
		return errors.New("key_directory_permissions_invalid")
	}
	defer syscall.Close(directory.fd)
	key := make([]byte, HMACKeyBytes)
	if _, err := io.ReadFull(random, key); err != nil {
		return errors.New("key_generation_failed")
	}
	fd, err := syscall.Openat(directory.fd, directory.name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("key_file_create_failed")
	}
	created := syscallFile{fd: fd}
	defer created.close()
	createdStat, err := validatedKeyStat(fd)
	if err != nil {
		return errors.New("key_file_permissions_invalid")
	}
	if err := created.writeAll(key); err != nil {
		// A partial inode is deliberately retained. Path-based cleanup could
		// unlink an attacker-replaced file; create-once and length validation
		// make the retained inode fail closed until explicit inspection.
		return errors.New("key_file_write_failed")
	}
	if err := syscall.Fsync(fd); err != nil {
		return errors.New("key_file_sync_failed")
	}
	if err := verifyNameBinding(directory.fd, directory.name, createdStat); err != nil {
		return errors.New("key_file_binding_changed")
	}
	if err := verifyDirectoryBinding(filepath.Dir(filepath.Clean(path)), directory.stat); err != nil {
		return errors.New("key_directory_binding_changed")
	}
	if err := syscall.Fsync(directory.fd); err != nil {
		return errors.New("key_directory_sync_failed")
	}
	return nil
}

func LoadHMACKeyFile(path string) ([]byte, error) {
	directory, err := openSecretDirectory(path)
	if err != nil {
		return nil, errors.New("key_directory_permissions_invalid")
	}
	defer syscall.Close(directory.fd)
	fd, err := syscall.Openat(directory.fd, directory.name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("key_file_open_failed")
	}
	file := syscallFile{fd: fd}
	defer file.close()
	openedStat, err := validatedKeyStat(fd)
	if err != nil {
		return nil, errors.New("key_file_permissions_invalid")
	}
	key := make([]byte, HMACKeyBytes+1)
	count := 0
	for count < len(key) {
		read, err := syscall.Read(fd, key[count:])
		if err != nil {
			return nil, errors.New("key_file_read_failed")
		}
		if read == 0 {
			break
		}
		count += read
	}
	if count != HMACKeyBytes {
		return nil, errors.New("key_file_length_invalid")
	}
	if err := verifyNameBinding(directory.fd, directory.name, openedStat); err != nil {
		return nil, errors.New("key_file_binding_changed")
	}
	if err := verifyDirectoryBinding(filepath.Dir(filepath.Clean(path)), directory.stat); err != nil {
		return nil, errors.New("key_directory_binding_changed")
	}
	return key[:HMACKeyBytes], nil
}

func openSecretDirectory(path string) (secretDirectory, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) || filepath.Base(cleaned) == "." || filepath.Base(cleaned) == string(filepath.Separator) {
		return secretDirectory{}, errors.New("key_path_must_be_absolute")
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(cleaned), string(filepath.Separator)), string(filepath.Separator))
	fd, err := syscall.Open(string(filepath.Separator), syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return secretDirectory{}, err
	}
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			syscall.Close(fd)
			return secretDirectory{}, errors.New("unsafe_directory_component")
		}
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			syscall.Close(fd)
			return secretDirectory{}, err
		}
		syscall.Close(fd)
		fd = next
		var stat syscall.Stat_t
		if syscall.Fstat(fd, &stat) != nil {
			syscall.Close(fd)
			return secretDirectory{}, errors.New("secret_directory_stat_failed")
		}
		worldWritableStickyRoot := stat.Uid == 0 && stat.Mode&0o022 != 0 && stat.Mode&syscall.S_ISVTX != 0
		if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR || (stat.Mode&0o022 != 0 && !worldWritableStickyRoot) || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			syscall.Close(fd)
			return secretDirectory{}, errors.New("unsafe_secret_directory")
		}
		if index == len(parts)-1 && (stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o777 != 0o700) {
			syscall.Close(fd)
			return secretDirectory{}, errors.New("unsafe_secret_leaf_directory")
		}
	}
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil {
		syscall.Close(fd)
		return secretDirectory{}, errors.New("secret_directory_stat_failed")
	}
	return secretDirectory{fd: fd, stat: stat, name: filepath.Base(cleaned)}, nil
}

func validatedKeyStat(fd int) (syscall.Stat_t, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return syscall.Stat_t{}, errors.New("unsafe_key_inode")
	}
	return stat, nil
}

func verifyNameBinding(directoryFD int, name string, expected syscall.Stat_t) error {
	fd, err := syscall.Openat(directoryFD, name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	actual, err := validatedKeyStat(fd)
	if err != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return errors.New("inode_changed")
	}
	return nil
}

func verifyDirectoryBinding(path string, expected syscall.Stat_t) error {
	probe, err := openSecretDirectory(filepath.Join(path, "binding.probe"))
	if err != nil {
		return err
	}
	defer syscall.Close(probe.fd)
	if probe.stat.Dev != expected.Dev || probe.stat.Ino != expected.Ino {
		return errors.New("directory_changed")
	}
	return nil
}

type syscallFile struct{ fd int }

func (f syscallFile) close() { _ = syscall.Close(f.fd) }

func (f syscallFile) writeAll(value []byte) error {
	for len(value) > 0 {
		written, err := syscall.Write(f.fd, value)
		if err != nil || written <= 0 {
			return errors.New("short_write")
		}
		value = value[written:]
	}
	return nil
}
