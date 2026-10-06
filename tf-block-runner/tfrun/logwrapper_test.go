package tfrun

import (
	"io"
	"log"
	"path"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The race detector fails this test when writes or callbacks overlap, as tofu's stdout and stderr do.
func Test_ConcurrentWrites_CountEveryByteAndRunCallbacksInSequence(t *testing.T) {
	l := NewLogWrap(log.New(io.Discard, "", 0), path.Join(t.TempDir(), "logs.txt"))
	require.NotNil(t, l)
	t.Cleanup(l.Close)
	callbacks := 0
	l.onWrite(func() { callbacks++ })

	const writesPerStream = 100
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range writesPerStream {
				_, _ = l.Write([]byte("line\n"))
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(2*writesPerStream*len("line\n")), l.logSize.Load())
	assert.Equal(t, 2*writesPerStream, callbacks)
}
