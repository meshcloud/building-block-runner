package tfrun

import (
	"io"
	"log"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_StopWhileIdle_WorkerStopsWithoutWaitingForTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rm := &DefaultRunManager{
			workerIn: make(chan workerToken, 1),
			shutdown: make(chan struct{}),
			logger:   log.New(io.Discard, "", 0),
		}
		start := time.Now()
		go rm.handoutWorkerToken(FAILED_WORKER_DELAY)
		synctest.Wait()

		rm.Stop()

		assert.Equal(t, stop, <-rm.workerIn)
		assert.Zero(t, time.Since(start))
	})
}
