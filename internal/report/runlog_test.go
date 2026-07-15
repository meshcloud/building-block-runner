package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunLog_OpensFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")

	l, err := NewRunLog(nil, path)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	assert.FileExists(t, path)
	assert.Equal(t, int64(0), l.Size())
}

func TestNewRunLog_ErrorOnUnwritableDirectory(t *testing.T) {
	// A path under a nonexistent directory: os.OpenFile fails, and NewRunLog must surface that
	// error rather than swallow it — a no-error signature would hide
	// exactly this failure mode.
	path := filepath.Join(t.TempDir(), "does-not-exist", "run.log")

	l, err := NewRunLog(nil, path)
	require.Error(t, err)
	assert.Nil(t, l)
}

func TestRunLog_WriteGrowsSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	n, err := l.Write([]byte("hello "))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, int64(6), l.Size())

	_, err = l.Write([]byte("world"))
	require.NoError(t, err)
	assert.Equal(t, int64(11), l.Size())
}

func TestRunLog_Write_ErrorDoesNotGrowSizeOrInvokeOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)
	require.NoError(t, l.Close()) // writing to a closed file forces a write error

	calls := 0
	l.OnWrite(func() { calls++ })

	_, err = l.Write([]byte("data"))
	require.Error(t, err)
	assert.Equal(t, int64(0), l.Size(), "a failed write must not be counted towards Size/LogStartIdx")
	assert.Equal(t, 0, calls, "onWrite must not fire for a write that never reached the file")
}

func TestRunLog_OnWrite_InvokedAfterEverySuccessfulWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	calls := 0
	l.OnWrite(func() { calls++ })

	_, _ = l.Write([]byte("a"))
	_, _ = l.Write([]byte("b"))

	assert.Equal(t, 2, calls)
}

func TestRunLog_Segment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	_, _ = l.Write([]byte("step one output\n"))
	idxAfterStepOne := l.Size()
	_, _ = l.Write([]byte("step two output\n"))

	ctx := context.Background()

	t.Run("from the start returns everything", func(t *testing.T) {
		assert.Equal(t, "step one output\nstep two output\n", l.Segment(ctx, 0))
	})

	t.Run("from a mid index returns only the later content", func(t *testing.T) {
		assert.Equal(t, "step two output\n", l.Segment(ctx, idxAfterStepOne))
	})

	t.Run("negative index is out of range", func(t *testing.T) {
		assert.Empty(t, l.Segment(ctx, -1))
	})

	t.Run("index past the end is out of range", func(t *testing.T) {
		assert.Empty(t, l.Segment(ctx, l.Size()+100))
	})
}

func TestRunLog_Segment_ReadErrorReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)

	// Close then remove the file out from under the RunLog to force a read error on Segment;
	// it must degrade to "" rather than panic (matches the tfrun predecessor's silent-empty
	// behavior for callers, now additionally logged).
	require.NoError(t, l.Close())
	require.NoError(t, os.Remove(path))

	assert.Empty(t, l.Segment(context.Background(), 0))
}

func TestRunLog_Close(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l, err := NewRunLog(nil, path)
	require.NoError(t, err)

	assert.NoError(t, l.Close())
}
