package report

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// RunLog is the file-backed live log for one run: an append-only "update log" file whose size
// is tracked so a step's LogStartIdx can later be read back as a Segment (the step's
// SystemMessage). Write/Size/Segment are kept on one cohesive type instead of a raw *os.File
// plus a size field tracked by the caller — this generalizes the tf-only logwrap
// predecessor for reuse by every runner that streams step output.
type RunLog struct {
	log  *slog.Logger
	file *os.File
	size int64
	// onWrite, when set, is invoked after every successful Write (e.g. to wake an observer
	// waiting on new log content). Optional — the zero value is a no-op.
	onWrite func()
}

// NewRunLog opens (creating if necessary) the update-log file at path in append mode.
// Deliberately returns an error rather than a no-error signature: file
// I/O can fail (fail fast, never swallow), and every existing caller of the tfrun
// predecessor already handles that error.
func NewRunLog(logger *slog.Logger, path string) (*RunLog, error) {
	if logger == nil {
		logger = slog.Default()
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening update-log file %q: %w", path, err)
	}

	return &RunLog{log: logger, file: f}, nil
}

// OnWrite registers the callback invoked after every successful Write.
func (l *RunLog) OnWrite(f func()) {
	l.onWrite = f
}

// Write appends p to the update-log file and grows Size() accordingly.
func (l *RunLog) Write(p []byte) (int, error) {
	n, err := l.file.Write(p)
	if err != nil {
		return n, err
	}

	l.size += int64(n)
	if l.onWrite != nil {
		l.onWrite()
	}

	return n, nil
}

// Size returns the number of bytes written so far — the value the next step's LogStartIdx
// should be stamped with.
func (l *RunLog) Size() int64 {
	return l.size
}

// Segment returns the log content written from startIdx up to the current Size(). It re-reads
// the file on every call: io.Seeker does not support Seek on a file opened in append mode, and
// per-step logs are small enough that this is not worth a more complex tracking scheme.
// On any read error, or an out-of-range startIdx, it logs a warning and returns "" — matching
// the tfrun predecessor's silent-empty-string behavior for callers, but now observable.
func (l *RunLog) Segment(ctx context.Context, startIdx int64) string {
	b, err := os.ReadFile(l.file.Name())
	if err != nil {
		l.log.WarnContext(ctx, "reading run log segment", "file", l.file.Name(), "error", err)
		return ""
	}
	if startIdx < 0 || startIdx > int64(len(b)) {
		l.log.WarnContext(ctx, "run log segment start index out of range", "file", l.file.Name(), "startIdx", startIdx, "size", len(b))
		return ""
	}
	return string(b[startIdx:])
}

// Close closes the underlying update-log file.
func (l *RunLog) Close() error {
	return l.file.Close()
}
