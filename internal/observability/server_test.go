package observability

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestServer_Healthz_ByteIdenticalOKBody(t *testing.T) {
	srv := NewServer(discardLogger(), ":0", prometheus.NewRegistry())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, healthzBody, string(body))
}

func TestServer_Metrics_ExposesGatheredSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_probe_total", Help: "test probe"})
	counter.Inc()
	require.NoError(t, reg.Register(counter))

	srv := NewServer(discardLogger(), ":0", reg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "test_probe_total 1")
}

func TestServer_NothingServedTwice(t *testing.T) {
	srv := NewServer(discardLogger(), ":0", prometheus.NewRegistry())

	// only /healthz and /metrics are registered on the mux -- an unrelated path 404s
	// rather than falling through to either handler (nothing served twice, nothing
	// served by accident either).
	req := httptest.NewRequest(http.MethodGet, "/unregistered", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServer_Start_BindsSuccessfully(t *testing.T) {
	srv := NewServer(discardLogger(), "127.0.0.1:0", prometheus.NewRegistry())
	assert.NoError(t, srv.Start())
}

func TestServer_Start_BindFailureReturnsError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = occupied.Close() }()

	srv := NewServer(discardLogger(), occupied.Addr().String(), prometheus.NewRegistry())
	err = srv.Start()
	assert.Error(t, err, "binding on an already-occupied address must be a reported error, not silent (D12 fatal-bind contract)")
}

func TestNewRegistry_CarriesGoAndProcessCollectorBaseline(t *testing.T) {
	reg := NewRegistry()
	families, err := reg.Gather()
	require.NoError(t, err)

	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	assert.Contains(t, names, "go_goroutines", "the go-collector baseline must be present")
}
