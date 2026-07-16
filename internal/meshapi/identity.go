package meshapi

import "fmt"

// Identity is the runner identity stamped onto every run-endpoint request as the
// User-Agent and X-Meshcloud-Runner-Name/-Version headers.
//
// It replaces the former package-level runnerName/runnerVersion globals + the
// SetClientMetadata setter (the last deferred mutable global in this module): identity is
// now passed per client via WithIdentity, so two clients in one process (e.g. a future
// cmd/bbrunner superset) can carry distinct identities and tests need no global reset.
//
// The zero value reproduces the old package defaults ("unknown-runner"/"dev") so a client
// constructed without WithIdentity stamps exactly what the un-initialised globals did.
type Identity struct {
	Name    string
	Version string
}

func (id Identity) name() string {
	if id.Name == "" {
		return "unknown-runner"
	}
	return id.Name
}

func (id Identity) version() string {
	if id.Version == "" {
		return "dev"
	}
	return id.Version
}

// UserAgent renders the User-Agent header value, byte-identical to the former
// userAgent() helper: "meshcloud-<name>/<version>".
func (id Identity) UserAgent() string {
	return fmt.Sprintf("meshcloud-%s/%s", id.name(), id.version())
}
