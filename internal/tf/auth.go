package tf

import (
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type AuthType string

const (
	AUTH_TYPE_NONE = "NONE"
	AUTH_TYPE_SSH  = "SSH"
)

type auth interface {
	name() AuthType
	prepare(dir string, log *logwrap) error
	toTransport(url string, log *logwrap) (transport.AuthMethod, error)
	done() error
}
