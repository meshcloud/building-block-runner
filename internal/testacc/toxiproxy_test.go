package testacc

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Shopify/toxiproxy/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

const (
	upstream   = "upstream"
	downstream = "downstream"
)

// toxicProxy forwards TCP on a port of its own, so that its toxics hit only the test that started it.
type toxicProxy struct {
	proxy       *toxiproxy.Proxy
	firstClient chan struct{}
}

// startToxicProxy stops when the test ends, so start it before anything that talks through it in a cleanup.
func startToxicProxy(t *testing.T, upstreamAddr string) *toxicProxy {
	t.Helper()
	p := &toxicProxy{firstClient: make(chan struct{})}
	accepted := sync.OnceFunc(func() { close(p.firstClient) })
	// The proxy tells of a new connection only in its log.
	logger := zerolog.New(io.Discard).Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, message string) {
		if message == "Accepted client" {
			accepted()
		}
	}))
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(nil), logger)
	p.proxy = toxiproxy.NewProxy(server, t.Name(), "127.0.0.1:0", upstreamAddr)
	require.NoError(t, p.proxy.Start())
	t.Cleanup(p.proxy.Stop)
	return p
}

func (s *accSuite) startApiProxy(t *testing.T) *toxicProxy {
	t.Helper()
	endpoint, err := url.Parse(s.endpoint)
	require.NoError(t, err)
	return startToxicProxy(t, endpoint.Host)
}

func (p *toxicProxy) addr() string {
	return p.proxy.Listen
}

func (p *toxicProxy) url() string {
	//goland:noinspection HttpUrlsUsage // a local proxy in front of the local meshStack
	return "http://" + p.addr()
}

func (p *toxicProxy) addToxic(t *testing.T, toxicType, stream string, attributes map[string]any) (remove func()) {
	t.Helper()
	name := toxicType + "_" + stream
	toxic, err := json.Marshal(map[string]any{"name": name, "type": toxicType, "stream": stream, "attributes": attributes})
	require.NoError(t, err)
	_, err = p.proxy.Toxics.AddToxicJson(strings.NewReader(string(toxic)))
	require.NoError(t, err)
	remove = sync.OnceFunc(func() {
		if err := p.proxy.Toxics.RemoveToxic(context.Background(), name); err != nil {
			t.Errorf("remove the toxic %s: %v", name, err)
		}
	})
	t.Cleanup(remove)
	return remove
}
