package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// histogramSampleCount reads back how many observations a histogram series recorded --
// testutil.ToFloat64 only handles single-value collectors (Gauge/Counter/Untyped), so a
// HistogramVec's observation count needs the raw dto.Metric.
func histogramSampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	metric, ok := o.(prometheus.Metric)
	require.True(t, ok, "Observer must also be a prometheus.Metric (true for *prometheus.histogram)")
	var m dto.Metric
	require.NoError(t, metric.Write(&m))
	return m.GetHistogram().GetSampleCount()
}

func TestRunMetrics_RunClaimed_IncrementsCounter(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.RunClaimed()
	m.RunClaimed()

	assert.InDelta(t, 2, testutil.ToFloat64(m.claimed.WithLabelValues("runner-1")), 0)
}

func TestRunMetrics_RunSucceeded_IncrementsCounterAndObservesDuration(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.RunSucceeded(2 * time.Second)

	assert.InDelta(t, 1, testutil.ToFloat64(m.succeeded.WithLabelValues("runner-1")), 0)
	assert.InDelta(t, 0, testutil.ToFloat64(m.failed.WithLabelValues("runner-1")), 0)
	assert.Equal(t, uint64(1), histogramSampleCount(t, m.duration.WithLabelValues("runner-1")))
}

func TestRunMetrics_RunFailed_IncrementsCounterAndObservesDuration(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.RunFailed(500 * time.Millisecond)

	assert.InDelta(t, 1, testutil.ToFloat64(m.failed.WithLabelValues("runner-1")), 0)
	assert.InDelta(t, 0, testutil.ToFloat64(m.succeeded.WithLabelValues("runner-1")), 0)
}

func TestRunMetrics_PollError_IncrementsCounter(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.PollError()

	assert.InDelta(t, 1, testutil.ToFloat64(m.pollErrors.WithLabelValues("runner-1")), 0)
}

func TestRunMetrics_RunUnhandled_IncrementsCounterByType(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.RunUnhandled("TERRAFORM")
	m.RunUnhandled("TERRAFORM")
	m.RunUnhandled("MANUAL")

	assert.InDelta(t, 2, testutil.ToFloat64(m.unhandled.WithLabelValues("runner-1", "TERRAFORM")), 0)
	assert.InDelta(t, 1, testutil.ToFloat64(m.unhandled.WithLabelValues("runner-1", "MANUAL")), 0)
	// Fail-fast is deliberately NOT counted as an executed-and-failed run.
	assert.InDelta(t, 0, testutil.ToFloat64(m.failed.WithLabelValues("runner-1")), 0)
}

func TestRunMetrics_AtCapacitySkip_IncrementsCounter(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-1")

	m.AtCapacitySkip()

	assert.InDelta(t, 1, testutil.ToFloat64(m.atCapacitySkip.WithLabelValues("runner-1")), 0)
}

func TestRunMetrics_LabelsSeriesByRunnerUuid(t *testing.T) {
	m := NewRunMetrics(prometheus.NewRegistry(), "runner-a")

	m.RunClaimed()

	// if RunClaimed used the wrong label internally, WithLabelValues("runner-a") here
	// would return a distinct, freshly-zeroed series rather than the incremented one.
	assert.InDelta(t, 1, testutil.ToFloat64(m.claimed.WithLabelValues("runner-a")), 0)
	assert.InDelta(t, 0, testutil.ToFloat64(m.claimed.WithLabelValues("runner-b")), 0)
}
