package runtime

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	minSecretBytes = 32
	maxSecretBytes = 4096
)

type SecretFiles struct {
	IngressBearer    string `json:"ingress_bearer"`
	ReadBearer       string `json:"read_bearer"`
	MutationBearer   string `json:"mutation_bearer"`
	CSRF             string `json:"csrf"`
	IdentityHMAC     string `json:"identity_hmac"`
	AuditHMAC        string `json:"audit_hmac"`
	DatabasePassword string `json:"database_password"`
}

type Secrets struct {
	IngressBearer    []byte
	ReadBearer       []byte
	MutationBearer   []byte
	CSRF             []byte
	IdentityHMAC     []byte
	AuditHMAC        []byte
	DatabasePassword []byte
}

func (s SecretFiles) entries() []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{"ingress_bearer", s.IngressBearer},
		{"read_bearer", s.ReadBearer},
		{"mutation_bearer", s.MutationBearer},
		{"csrf", s.CSRF},
		{"identity_hmac", s.IdentityHMAC},
		{"audit_hmac", s.AuditHMAC},
		{"database_password", s.DatabasePassword},
	}
}

func (s SecretFiles) ValidateLocators() error {
	seen := map[string]bool{}
	for _, entry := range s.entries() {
		if !filepath.IsAbs(entry.path) || entry.path == "/" || seen[entry.path] {
			return errors.New("secret_file_locator_invalid")
		}
		seen[entry.path] = true
	}
	return nil
}

func LoadSecretFiles(files SecretFiles) (Secrets, error) {
	if err := files.ValidateLocators(); err != nil {
		return Secrets{}, err
	}
	values := make([][]byte, 0, len(files.entries()))
	for _, entry := range files.entries() {
		value, err := readSecretFile(entry.path)
		if err != nil {
			return Secrets{}, fmt.Errorf("%s_secret_invalid", entry.name)
		}
		for _, existing := range values {
			if constantBytesEqual(value, existing) {
				return Secrets{}, errors.New("secret_values_must_be_pairwise_distinct")
			}
		}
		values = append(values, value)
	}
	return Secrets{
		IngressBearer: values[0], ReadBearer: values[1], MutationBearer: values[2],
		CSRF: values[3], IdentityHMAC: values[4], AuditHMAC: values[5],
		DatabasePassword: values[6],
	}, nil
}

func readSecretFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("secret_must_be_regular_file")
	}
	permissions := info.Mode().Perm()
	if filepath.Dir(path) == "/run/secrets" {
		if permissions&0o022 != 0 {
			return nil, errors.New("compose_secret_is_writable")
		}
	} else if permissions != 0o600 {
		return nil, errors.New("secret_mode_must_be_0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil || len(raw) > maxSecretBytes {
		return nil, errors.New("secret_read_failed")
	}
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	if len(raw) < minSecretBytes || bytes.ContainsAny(raw, "\x00\r\n") {
		return nil, errors.New("secret_length_or_framing_invalid")
	}
	return append([]byte(nil), raw...), nil
}

func constantBytesEqual(left, right []byte) bool {
	leftDigest := sha256.Sum256(left)
	rightDigest := sha256.Sum256(right)
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}
