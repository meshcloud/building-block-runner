package tf

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

type NoAuth struct{}

func (noAuth *NoAuth) name() AuthType {
	return AUTH_TYPE_NONE
}

func (noAuth *NoAuth) prepare(dir string, log *logwrap) error {
	log.PrintlnToLocalLogs(fmt.Sprintf("NoAuth perpare (dir '%s')", dir))
	return nil
}

func (noAuth *NoAuth) done() error {
	return nil
}

func (noAuth *NoAuth) toTransport(url string, log *logwrap) (transport.AuthMethod, error) {
	if strings.HasPrefix(url, "git@") {
		return nil, errors.New("cannot clone via ssh without auth. Please add a .pem file, that contains a private key to your source")
	} else {
		return nil, nil // no transport auth is needed.
	}
}
