//go:build unix

package codexadapter

import (
	"os"
	"strconv"
	"syscall"
)

// platformFileID returns a stable "device:inode" string for path when the
// host filesystem exposes one via syscall.Stat_t, and "" otherwise -- never
// a fabricated value. This is strictly an availability-where-possible aid to
// RolloutFileIdentity; ComputeRolloutFileIdentity's PathPseudonym is the
// authoritative, always-present identity component.
func platformFileID(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(stat.Ino, 10)
}
