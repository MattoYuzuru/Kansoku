package claudeadapter

// sourceIDTranscript identifies contracts/claude/transcript-and-inventory.yaml's
// claude.transcript source. The checkpointed JSONL importer itself (file
// identity, offset/rotation/truncation handling, historical-content opt-in)
// is a distinct source with its own dedicated implementation stage; this
// constant exists now only so this stage's reconciliation logic
// (reconcile.go) can name claude.transcript as a lane input alongside
// claude.hook/claude.otel without inventing a parallel, undeclared source
// identifier.
const sourceIDTranscript = "claude.transcript"
