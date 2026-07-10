package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// Port is a resolved TCP port number for a management listener (D12). The named type
// pushes port-vs-arbitrary-int mixups (e.g. swapping a port and a timeout, both bare
// ints) to the compiler at every ManagementPort call site.
type Port uint16

// Addr formats p as a net.Listen("tcp", ...) bind address, e.g. Port(8100).Addr() ==
// ":8100".
func (p Port) Addr() string {
	return fmt.Sprintf(":%d", p)
}

// managementPortEnv is the primary env var every persona checks first (D12). It is
// additive, not a legacy alias -- introduced fresh in phase 4, so it carries no
// deprecation baggage of its own. Persona-specific legacy aliases (e.g. tf's PORT) are
// supplied by the caller via the aliases parameter.
const managementPortEnv = "MANAGEMENT_PORT"

// ManagementPort resolves the management listener's port with precedence
// MANAGEMENT_PORT > aliases (in order, first one set wins) > def -- e.g. for the tf
// persona, `ManagementPort(log, 8100, EnvAlias{Var: "PORT", Deprecated: true})` keeps
// every existing deployment's resolved port unchanged (D10) while introducing the new
// canonical var (D12).
//
// A value that is set but not a valid, non-zero uint16 port is a startup-fatal
// misconfiguration (P5): silently falling back to def would let a typo'd override boot
// the wrong listener with nobody noticing (the mux's envOrInt precedent).
func ManagementPort(log *slog.Logger, def Port, aliases ...EnvAlias) (Port, error) {
	candidates := make([]EnvAlias, 0, len(aliases)+1)
	candidates = append(candidates, EnvAlias{Var: managementPortEnv})
	candidates = append(candidates, aliases...)

	for _, alias := range candidates {
		v, ok := os.LookupEnv(alias.Var)
		if !ok || v == "" {
			continue
		}
		if alias.Deprecated {
			WarnDeprecated(log, alias.Var, managementPortEnv)
		}
		port, err := parsePort(v)
		if err != nil {
			return 0, fmt.Errorf("%s=%q is not a valid management port: %w", alias.Var, v, err)
		}
		return port, nil
	}
	return def, nil
}

func parsePort(v string) (Port, error) {
	n, err := strconv.ParseUint(v, 10, 16)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("port 0 is not usable")
	}
	return Port(n), nil
}
