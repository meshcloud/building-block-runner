package tfrun

import (
	"fmt"
	"log"
	"os"
)

type logwrap struct {
	logger       *log.Logger
	updateLogger *os.File
	logSize      int64
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
		logSize:      0,
		callback:     func() {},
	}
}

// this writes to the tf output log file
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
