//go:build !linux

package privacy

import (
	"errors"
	"io"
)

const HMACKeyBytes = 32

// The fd-relative implementation currently has a verified syscall surface on
// Linux only. Other Unix and non-Unix systems fail closed until an equivalent
// openat/inode-binding implementation or OS keychain backend is reviewed.
func CreateHMACKeyFile(string, io.Reader) error {
	return errors.New("secure_keyfile_backend_unsupported")
}
func LoadHMACKeyFile(string) ([]byte, error) {
	return nil, errors.New("secure_keyfile_backend_unsupported")
}
