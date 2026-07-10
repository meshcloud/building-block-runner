package tf

import (
	"fmt"
	"log/slog"
	"os"
)

type logwrap struct {
	// logger is process ("local") logging only. The wire-visible update log is written
	// directly to updateLogger below via fmt.Sprint (never through slog), so migrating this
	// field to slog cannot alter step SystemMessage bytes (§8.3 SystemMessage hazard).
	logger       *slog.Logger
	updateLogger *os.File
	logSize      int64
	callback     func()
}

func NewLogWrap(logger *slog.Logger, logsFileName string) (*logwrap, error) {
	outlog, err := os.OpenFile(logsFileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening update-logs file %q: %w", logsFileName, err)
	}

	return &logwrap{
		logger:       logger,
		updateLogger: outlog,
		logSize:      0,
		callback:     func() {},
	}, nil
}

// this writes to the tf output log file.
func (l *logwrap) Write(p []byte) (int, error) {
	n, err := l.updateLogger.Write(p)
	if err != nil {
		return n, err
	} else {
		l.logSize = l.logSize + int64(n)
		l.callback() // inform that there are new logs for updates
		return n, err
	}
}

func (l *logwrap) PrintlnToLocalLogs(v ...any) {
	l.logger.Info(fmt.Sprint(v...))
}

func (l *logwrap) PrintlnToUpdateLogs(v ...any) (n int, err error) {
	return l.Write([]byte(fmt.Sprint(v...) + "\n"))
}

func (l *logwrap) PrintlnToLocalAndUpdateLogs(v ...any) {
	l.PrintlnToLocalLogs(v...)
	// The update-log write error (if any) already reaches the caller through every other
	// PrintlnToUpdateLogs call site; this convenience wrapper has no error return of its own
	// to propagate it through, and the local-log write above already recorded the message.
	_, _ = l.PrintlnToUpdateLogs(v...)
}

func (l *logwrap) Close() {
	_ = l.updateLogger.Close()
}
