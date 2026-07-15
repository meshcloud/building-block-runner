package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// FailOnUnconsumedLegacyEnv scans the process environment for variables whose name
// starts with one of the given legacy prefixes (e.g. "BLOCKRUNNER_", the Spring
// relaxed-binding holdover -- the Go ports do not reimplement Spring's relaxed-binding
// matrix) that were never consumed by a Path/Env call or a ${VAR} interpolation
// during Load. Call it once, after every Path/Load/Env call for a runner type's startup.
//
// A relaxed-binding holdover this loader does not recognize must surface as a hard
// startup error -- never as a runner that boots on wrong defaults and polls
// forever.
func (l *Loader) FailOnUnconsumedLegacyEnv(prefixes ...string) error {
	if len(prefixes) == 0 {
		return nil
	}

	var offending []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !hasAnyPrefix(name, prefixes) {
			continue
		}
		if l.consumed[name] {
			continue
		}
		offending = append(offending, name)
	}
	if len(offending) == 0 {
		return nil
	}
	sort.Strings(offending)
	return fmt.Errorf(
		"unrecognized legacy-prefixed environment variable(s) %s: not bound to any config key or ${VAR} interpolation -- check for a typo or a stale deployment config",
		strings.Join(offending, ", "),
	)
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
