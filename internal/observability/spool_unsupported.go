//go:build !linux

package observability

import "errors"

func validateSpoolPath(string) error                { return errors.New("secure_spool_unsupported") }
func appendSecureSpool(string, []byte, int64) error { return errors.New("secure_spool_unsupported") }
func readSecureSpool(string, int64) ([]byte, error) {
	return nil, errors.New("secure_spool_unsupported")
}
func drainSecureSpool(string) error { return errors.New("secure_spool_unsupported") }
