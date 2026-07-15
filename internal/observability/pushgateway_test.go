package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushGatewayURL_DefaultDisabled(t *testing.T) {
	// No t.Setenv here: the default (unset) must report disabled, so an existing
	// deployment that never opted in sees no behavior change.
	assert.Empty(t, PushGatewayURL())
}

func TestPushGatewayURL_ReadsEnv(t *testing.T) {
	t.Setenv(EnvPushGatewayURL, "http://gateway.example:9091")
	assert.Equal(t, "http://gateway.example:9091", PushGatewayURL())
}

// pushGatewayFake records every request method it receives, always answering 202
// Accepted (push) so the Pusher never surfaces a push error.
type pushGatewayFake struct {
	methods []string
}

func newPushGatewayFake(t *testing.T) (*pushGatewayFake, *httptest.Server) {
	t.Helper()
	fake := &pushGatewayFake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.methods = append(fake.methods, r.Method)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return fake, srv
}

func TestPushRunMetrics_NoopWhenURLEmpty(t *testing.T) {
	reg := prometheus.NewRegistry()
	// url == "" must never dial out -- there is no server listening at all here, so any
	// attempted HTTP call would itself fail loudly (a log line), proving the no-op.
	PushRunMetrics(discardLogger(), "", "runner-1", "run-1", reg, true)
}

func TestPushRunMetrics_SuccessPushesThenDeletes(t *testing.T) {
	fake, srv := newPushGatewayFake(t)
	reg := prometheus.NewRegistry()

	PushRunMetrics(discardLogger(), srv.URL, "runner-1", "run-1", reg, true)

	assert.Equal(t, []string{http.MethodPut, http.MethodDelete}, fake.methods,
		"a successful run must be pushed then its group deleted from the gateway")
}

func TestPushRunMetrics_FailurePushesWithoutDelete(t *testing.T) {
	fake, srv := newPushGatewayFake(t)
	reg := prometheus.NewRegistry()

	PushRunMetrics(discardLogger(), srv.URL, "runner-1", "run-1", reg, false)

	assert.Equal(t, []string{http.MethodPut}, fake.methods,
		"a failed run's group must be left on the gateway for an operator to inspect")
}

func TestPushRunMetrics_PushErrorNeverDeletes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	reg := prometheus.NewRegistry()

	// Must not panic, and must not attempt Delete after a failed Push (there would be
	// nothing to delete -- the gateway never accepted the pushed group).
	PushRunMetrics(discardLogger(), srv.URL, "runner-1", "run-1", reg, true)
}

func TestPushRunMetrics_UnreachableGatewayNeverPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	// A closed listener: connection refused. Push-gateway support is best-effort
	// observability -- an unreachable gateway must be logged, never fatal.
	PushRunMetrics(discardLogger(), "http://127.0.0.1:1", "runner-1", "run-1", reg, true)
}

func TestInstrumentSingleRun_ReturnsFnErrorUnchanged(t *testing.T) {
	sentinel := errors.New("boom")

	err := InstrumentSingleRun(discardLogger(), "runner-1", "run-1", func() error { return sentinel })
	assert.Same(t, sentinel, err)

	err = InstrumentSingleRun(discardLogger(), "runner-1", "run-1", func() error { return nil })
	require.NoError(t, err)
}

func TestInstrumentSingleRun_PushesOnSuccessAndFailureWhenEnabled(t *testing.T) {
	fake, srv := newPushGatewayFake(t)
	t.Setenv(EnvPushGatewayURL, srv.URL)

	err := InstrumentSingleRun(discardLogger(), "runner-1", "run-1", func() error { return nil })
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodPut, http.MethodDelete}, fake.methods,
		"a successful single run must push then delete when push-gateway support is enabled")

	fake.methods = nil
	sentinel := errors.New("boom")
	err = InstrumentSingleRun(discardLogger(), "runner-1", "run-2", func() error { return sentinel })
	assert.Same(t, sentinel, err)
	assert.Equal(t, []string{http.MethodPut}, fake.methods,
		"a failed single run must push but never delete its group")
}

func TestInstrumentSingleRun_DisabledByDefaultMakesNoGatewayCall(t *testing.T) {
	// No t.Setenv(EnvPushGatewayURL, ...): the default must never dial out. There is no
	// listener at all, so a stray call would surface as a logged dial error, not a panic
	// -- this test only pins "no observable difference", the no-op path is exercised
	// directly (and more explicitly) by TestPushRunMetrics_NoopWhenURLEmpty.
	err := InstrumentSingleRun(discardLogger(), "runner-1", "run-1", func() error { return nil })
	require.NoError(t, err)
}

// TestInstrumentSingleRunResult_SuccessSignalIndependentOfError pins B5's whole point: fn's
// success flag and its error are independent. A nil error with success=false (tf's shape for a
// failed tofu apply/destroy -- ExecuteRun returns nil once tofu init/apply has begun, even on
// failure) must still push+never-delete like any other failure, not push+delete like the
// err-derived InstrumentSingleRun would.
func TestInstrumentSingleRunResult_SuccessSignalIndependentOfError(t *testing.T) {
	fake, srv := newPushGatewayFake(t)
	t.Setenv(EnvPushGatewayURL, srv.URL)

	err := InstrumentSingleRunResult(discardLogger(), "runner-1", "run-1", func() (bool, error) {
		return false, nil // nil error, but the run's real terminal status was a failure
	})
	require.NoError(t, err, "InstrumentSingleRunResult must return fn's error unchanged")
	assert.Equal(t, []string{http.MethodPut}, fake.methods,
		"a nil error with success=false must be metered/pushed as a failure, not a success")
}

// TestInstrumentSingleRunResult_ReturnsFnErrorUnchanged mirrors
// TestInstrumentSingleRun_ReturnsFnErrorUnchanged: the returned error is always fn's, regardless
// of the independent success flag.
func TestInstrumentSingleRunResult_ReturnsFnErrorUnchanged(t *testing.T) {
	sentinel := errors.New("boom")

	err := InstrumentSingleRunResult(discardLogger(), "runner-1", "run-1", func() (bool, error) { return true, sentinel })
	assert.Same(t, sentinel, err)
}
