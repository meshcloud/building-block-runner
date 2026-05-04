package meshapi

import "encoding/base64"

// AuthProvider supplies the Authorization header value for outgoing API requests.
type AuthProvider interface {
	AuthHeader() string
}

// BasicAuth implements AuthProvider using HTTP Basic authentication.
type BasicAuth struct {
	Username string
	Password string
}

func (a BasicAuth) AuthHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password))
}

// BearerTokenAuth implements AuthProvider using a Bearer token.
type BearerTokenAuth struct {
	Token string
}

func (a BearerTokenAuth) AuthHeader() string {
	return "Bearer " + a.Token
}
