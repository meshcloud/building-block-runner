package testmeshstack

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/hal"
)

type StatusServer struct {
	URL string
}

func NewStatusServer(t *testing.T) *StatusServer {
	t.Helper()
	s := &StatusServer{}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

func (s *StatusServer) Link(run *client.MeshBuildingBlockRun) {
	run.Spec.RunToken = Token
	run.Links = hal.Links{
		"meshstackBaseUrl": {Href: s.URL},
		"registerSource":   {Href: s.URL + "/source"},
		"updateSource":     {Href: s.URL + "/source/{sourceId}"},
	}
}

func (s *StatusServer) serve(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{}`))
}
