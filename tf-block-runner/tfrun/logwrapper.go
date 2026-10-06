package tfrun

import (
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
)

// logwrap is written from two goroutines at once while tofu runs: tfexec copies its stdout and its stderr
// separately. writeMu keeps the writes and the callbacks, which change the run status, in sequence.
type logwrap struct {
	logger       *log.Logger
	updateLogger *os.File
	logSize      atomic.Int64
	writeMu      sync.Mutex
	callback     func()
}

func NewLogWrap(logger *log.Logger, logsFileName string) *logwrap {
	outlog, err := os.OpenFile(logsFileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil
	}

	return &logwrap{
		logger:       logger,
		updateLogger: outlog,
		callback:     func() {},
	}
}

// this writes to the tf output log file
func (l *logwrap) Write(p []byte) (int, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	n, err := l.updateLogger.Write(p)
	if err != nil {
		return n, err
	}
	l.logSize.Add(int64(n))
	l.callback() // inform that there are new logs for updates
	return n, nil
}

func (l *logwrap) onWrite(callback func()) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.callback = callback
}

func (l *logwrap) PrintlnToLocalLogs(v ...any) {
	l.logger.Println(v...)
}

func (l *logwrap) PrintlnToUpdateLogs(v ...any) (n int, err error) {
	return l.Write([]byte(fmt.Sprint(v...) + "\n"))
}

func (l *logwrap) PrintlnToLocalAndUpdateLogs(v ...any) {
	l.PrintlnToLocalLogs(v...)
	l.PrintlnToUpdateLogs(v...)
}

func (l *logwrap) Close() {
	l.updateLogger.Close()
}
