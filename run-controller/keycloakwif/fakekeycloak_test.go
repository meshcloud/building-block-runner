package keycloakwif

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fakeRealm        = "meshcloud-dev-wif"
	fakeAdminId      = "run-controller"
	fakeAdminSecret  = "admin-secret"
	fakeScopeBasic   = "scope-basic"
	fakeScopeService = "scope-service-account"
)

type fakeClient struct {
	clientId       string
	secret         string
	attributes     map[string]string
	claims         map[string]string
	defaultScopes  []string
	optionalScopes []string
}

// Like a real realm, fakeKeycloak gives every new client the default scopes, whatever the request asks for.
type fakeKeycloak struct {
	server *httptest.Server
	now    func() time.Time

	mu            sync.Mutex
	clients       map[string]*fakeClient
	nextId        int
	adminLogins   int
	adminToken    string
	failRunTokens int
	maxLifetime   time.Duration
	extraClaims   map[string]string
}

func newFakeKeycloak(t *testing.T, now func() time.Time) *fakeKeycloak {
	f := &fakeKeycloak{clients: map[string]*fakeClient{}, now: now}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeKeycloak) issuer() string {
	return f.server.URL + "/realms/" + fakeRealm
}

func (f *fakeKeycloak) clientIds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, client := range f.clients {
		ids = append(ids, client.clientId)
	}
	return ids
}

// Like a realm whose issuer changed: the admin token that was handed out is no longer accepted.
func (f *fakeKeycloak) revokeAdminToken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adminToken = ""
}

func (f *fakeKeycloak) addClient(clientId string, attributes map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextId++
	f.clients[strconv.Itoa(f.nextId)] = &fakeClient{clientId: clientId, attributes: attributes}
}

func (f *fakeKeycloak) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	tokenPath := "/realms/" + fakeRealm + "/protocol/openid-connect/token"
	adminPrefix := "/admin/realms/" + fakeRealm + "/clients"
	if r.URL.Path == tokenPath {
		f.token(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, adminPrefix) {
		http.NotFound(w, r)
		return
	}
	if f.adminToken == "" || r.Header.Get("Authorization") != "Bearer "+f.adminToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, adminPrefix), "/"), "/")
	switch {
	case parts[0] == "" && r.Method == http.MethodPost:
		f.createClient(w, r)
	case parts[0] == "" && r.Method == http.MethodGet:
		f.listClients(w, r)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		f.deleteClient(w, parts[0])
	case len(parts) == 2 && r.Method == http.MethodGet:
		f.listScopes(w, parts[0], parts[1])
	case len(parts) == 3 && r.Method == http.MethodDelete:
		f.removeScope(w, parts[0], parts[1], parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKeycloak) token(w http.ResponseWriter, r *http.Request) {
	clientId, secret := r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	if clientId == fakeAdminId && secret == fakeAdminSecret {
		f.adminLogins++
		f.adminToken = fmt.Sprintf("admin-access-token-%d", f.adminLogins)
		writeJSON(w, http.StatusOK, map[string]any{"access_token": f.adminToken, "expires_in": 300})
		return
	}
	var client *fakeClient
	for _, c := range f.clients {
		if c.clientId == clientId && c.secret == secret {
			client = c
		}
	}
	if client == nil || f.failRunTokens > 0 {
		f.failRunTokens--
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized_client", "request": "client_secret=" + secret})
		return
	}

	lifespan, _ := strconv.Atoi(client.attributes["access.token.lifespan"])
	lifetime := time.Duration(lifespan) * time.Second
	if f.maxLifetime > 0 {
		lifetime = min(lifetime, f.maxLifetime)
	}
	claims := map[string]any{
		"iss": f.issuer(),
		"sub": "service-account-user-id",
		"exp": f.now().Add(lifetime).Unix(),
		"iat": f.now().Unix(),
		"jti": "jti",
		"typ": "Bearer",
		"azp": client.clientId,
	}
	for claim, value := range client.claims {
		claims[claim] = value
	}
	for _, scope := range client.defaultScopes {
		if scope == fakeScopeService {
			claims["client_id"] = client.clientId
			claims["clientHost"] = "10.0.0.1"
			claims["clientAddress"] = "10.0.0.1"
		}
	}
	for claim, value := range f.extraClaims {
		claims[claim] = value
	}
	payload, _ := json.Marshal(claims)
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "expires_in": lifespan})
}

func (f *fakeKeycloak) createClient(w http.ResponseWriter, r *http.Request) {
	var representation struct {
		ClientId        string            `json:"clientId"`
		Secret          string            `json:"secret"`
		Attributes      map[string]string `json:"attributes"`
		ProtocolMappers []struct {
			ProtocolMapper string            `json:"protocolMapper"`
			Config         map[string]string `json:"config"`
		} `json:"protocolMappers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&representation); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	claims := map[string]string{}
	for _, mapper := range representation.ProtocolMappers {
		switch mapper.ProtocolMapper {
		case "oidc-hardcoded-claim-mapper":
			claims[mapper.Config["claim.name"]] = mapper.Config["claim.value"]
		case "oidc-audience-mapper":
			claims["aud"] = mapper.Config["included.custom.audience"]
		}
	}
	f.nextId++
	id := strconv.Itoa(f.nextId)
	f.clients[id] = &fakeClient{
		clientId:       representation.ClientId,
		secret:         representation.Secret,
		attributes:     representation.Attributes,
		claims:         claims,
		defaultScopes:  []string{fakeScopeBasic, fakeScopeService},
		optionalScopes: []string{"scope-email"},
	}
	w.Header().Set("Location", f.server.URL+"/admin/realms/"+fakeRealm+"/clients/"+id)
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeKeycloak) listClients(w http.ResponseWriter, r *http.Request) {
	first, _ := strconv.Atoi(r.URL.Query().Get("first"))
	max, _ := strconv.Atoi(r.URL.Query().Get("max"))
	var matching []map[string]any
	for id := 1; id <= f.nextId; id++ {
		client, ok := f.clients[strconv.Itoa(id)]
		if ok && strings.Contains(client.clientId, r.URL.Query().Get("clientId")) {
			matching = append(matching, map[string]any{"id": strconv.Itoa(id), "clientId": client.clientId, "attributes": client.attributes})
		}
	}
	end := min(first+max, len(matching))
	writeJSON(w, http.StatusOK, matching[min(first, end):end])
}

func (f *fakeKeycloak) deleteClient(w http.ResponseWriter, id string) {
	if _, ok := f.clients[id]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	delete(f.clients, id)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeKeycloak) listScopes(w http.ResponseWriter, id, kind string) {
	client, ok := f.clients[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	scopes := []map[string]string{}
	for _, scope := range *client.scopes(kind) {
		scopes = append(scopes, map[string]string{"id": scope, "name": scope})
	}
	writeJSON(w, http.StatusOK, scopes)
}

func (f *fakeKeycloak) removeScope(w http.ResponseWriter, id, kind, scopeId string) {
	client, ok := f.clients[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	scopes := client.scopes(kind)
	for i, scope := range *scopes {
		if scope == scopeId {
			*scopes = append((*scopes)[:i], (*scopes)[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (c *fakeClient) scopes(kind string) *[]string {
	if kind == "default-client-scopes" {
		return &c.defaultScopes
	}
	return &c.optionalScopes
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(fmt.Sprintf("encode fake Keycloak response: %v", err))
	}
}
