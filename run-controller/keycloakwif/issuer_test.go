package keycloakwif

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_KeycloakRedirects_SecretNotSentElsewhere(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the redirect was followed")
	}))
	t.Cleanup(elsewhere.Close)
	keycloak := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusPermanentRedirect))
	t.Cleanup(keycloak.Close)
	issuer := NewIssuer(Config{Url: keycloak.URL, Realm: "wif-test", ClientId: "run-controller", ClientSecret: "secret"},
		http.DefaultClient, log.New(io.Discard, "", 0))

	_, err := issuer.Issue(context.Background(), "run-1", testSubject)

	require.ErrorContains(t, err, "HTTP 308")
}
