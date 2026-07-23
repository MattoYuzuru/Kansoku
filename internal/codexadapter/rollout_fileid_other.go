//go:build !unix

package codexadapter

// platformFileID has no portable inode/file-id accessor on this platform;
// it returns "" (never fabricated) so RolloutFileIdentity falls back to its
// always-present PathPseudonym component alone, matching
// contracts/codex/rollout-and-inventory.yaml's "inode_or_file_id_where_available"
// wording exactly.
func platformFileID(path string) string { return "" }
