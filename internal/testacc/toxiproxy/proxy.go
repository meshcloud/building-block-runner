package toxiproxy

import (
	"context"
	"encoding/json/v2"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Shopify/toxiproxy/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

const (
	Upstream   = "upstream"
	Downstream = "downstream"
)

// Proxy forwards TCP on a port of its own, so that its toxics hit only the test that started it.
type Proxy struct {
	Addr        string
	Url         string
	FirstClient <-chan struct{}

	proxy *toxiproxy.Proxy
}

// Start stops the proxy when the test ends, so start it before anything that talks through it in a cleanup.
func Start(t *testing.T, upstreamAddr string) Proxy {
	t.Helper()
	firstClient := make(chan struct{})
	accepted := sync.OnceFunc(func() { close(firstClient) })
	// The proxy tells of a new connection only in its log.
	logger := zerolog.New(io.Discard).Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, message string) {
		if message == "Accepted client" {
			accepted()
		}
	}))
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(nil), logger)
	proxy := toxiproxy.NewProxy(server, t.Name(), "127.0.0.1:0", upstreamAddr)
	require.NoError(t, proxy.Start())
	t.Cleanup(proxy.Stop)
	//goland:noinspection HttpUrlsUsage // a local proxy in front of the local meshStack
	return Proxy{Addr: proxy.Listen, Url: "http://" + proxy.Listen, FirstClient: firstClient, proxy: proxy}
}

func (p Proxy) AddToxic(t *testing.T, toxicType, stream string, attributes map[string]any) (remove func()) {
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
