package observability

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type ImportResult struct {
	Accepted int
	Replayed int
	Offset   int64
	Sequence uint64
}

// ImportTranscript opens the source read-only. A checkpoint is committed in
// the same FileStore transaction as each accepted fact/evidence pair.
func (i *Ingestor) ImportTranscript(path, importerID string) (ImportResult, error) {
	if importerID == "" {
		return ImportResult{}, errors.New("invalid_importer_id")
	}
	file, err := os.Open(path)
	if err != nil {
		return ImportResult{}, errors.New("import_open_failure")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ImportResult{}, errors.New("import_not_regular")
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ImportResult{}, errors.New("import_identity_failure")
	}
	canonicalPath, err = filepath.Abs(canonicalPath)
	if err != nil {
		return ImportResult{}, errors.New("import_identity_failure")
	}
	fileID := i.keyedIdentity("import-file/1", importerID+"\x00"+canonicalPath)
	checkpoint := i.store.Snapshot().Checkpoints[importerID]
	if checkpoint.FileID != "" && checkpoint.FileID != fileID {
		return ImportResult{}, errors.New("import_source_changed")
	}
	if checkpoint.Offset < 0 || checkpoint.Offset > info.Size() {
		return ImportResult{}, errors.New("invalid_checkpoint")
	}
	if _, err := file.Seek(checkpoint.Offset, io.SeekStart); err != nil {
		return ImportResult{}, errors.New("import_seek_failure")
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	result := ImportResult{Offset: checkpoint.Offset, Sequence: checkpoint.Sequence}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if len(line) > int(i.sanitizerLimits().MaxTotalBytes) {
				return result, errors.New("import_line_oversized")
			}
			result.Sequence++
			result.Offset += int64(len(line))
			nextCheckpoint := &Checkpoint{ImporterID: importerID, Offset: result.Offset, Sequence: result.Sequence, FileID: fileID}
			commit, ingestErr := i.ingestJSON(line, SourceTranscript, result.Sequence, nextCheckpoint)
			if ingestErr != nil {
				return result, ingestErr
			}
			if commit.DuplicateReplay {
				result.Replayed++
			} else {
				result.Accepted++
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, errors.New("import_read_failure")
		}
	}
	return result, nil
}

func (i *Ingestor) sanitizerLimits() privacyLimits {
	// The sanitizer itself enforces the authoritative bounds. This duplicate
	// fixed value prevents bufio from accumulating an unbounded JSONL line.
	return privacyLimits{MaxTotalBytes: 1 << 20}
}

type privacyLimits struct{ MaxTotalBytes int64 }
