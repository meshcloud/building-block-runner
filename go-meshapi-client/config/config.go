// Package config is the shared D7 config-loading mechanics for every runner persona:
// two-YAML-layer deep-merge (shared base overlaid by a per-persona/per-impl file),
// ${VAR} interpolation inside those YAML layers, explicit env-var bindings (highest
// precedence), and a fail-fast guard against unrecognized legacy-prefixed env vars.
//
// Persona config *structs* stay in their persona packages (tf keeps its keys, the
// controller keeps ControllerConfig incl. the file-only implementations map) — this
// package owns only the mechanics and the genuinely shared Api section (api.go).
package config

import (
	"fmt"
	"os"
	"regexp"
)

// Loader deep-merges the two config YAML layers and tracks which environment variable
// names it has "consumed" — via ${VAR} interpolation inside a YAML layer or via an
// explicit Env/Path binding — so FailOnUnconsumedLegacyEnv can tell a recognized
// override apart from a Spring relaxed-binding holdover nothing here understands (D7).
//
// A Loader is not safe for concurrent use; each persona's startup constructs and uses
// exactly one, sequentially, which matches how config loading happens today.
type Loader struct {
	consumed map[string]bool
}

// NewLoader returns a Loader ready for one persona's startup sequence.
func NewLoader() *Loader {
	return &Loader{consumed: map[string]bool{}}
}

func (l *Loader) markConsumed(name string) {
	if name == "" {
		return
	}
	l.consumed[name] = true
}

// interpolationPattern matches ${VAR}: a dollar-brace-wrapped shell-style identifier.
// Intentionally minimal (D7 high-level: "${VAR} interpolation", not a default-value
// expression language) -- an unset VAR resolves to the empty string, exactly like an
// unset shell variable would.
var interpolationPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolate replaces every ${VAR} occurrence in raw with os.Getenv(VAR) and records
// VAR as consumed -- the declarative mapping in the YAML *is* the consumption, whether
// or not the operator happens to have that variable set (D7: "the base runner-config.yml
// maps legacy env vars onto struct keys declaratively").
func (l *Loader) interpolate(raw []byte) []byte {
	return interpolationPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := string(interpolationPattern.FindSubmatch(match)[1])
		l.markConsumed(name)
		return []byte(os.Getenv(name))
	})
}

// readLayer reads path (empty path = no layer configured for this persona) and
// interpolates it. A missing file is tolerated (many personas run on defaults+env
// alone) and reported via ok=false, not err.
func (l *Loader) readLayer(path string) (raw []byte, ok bool, err error) {
	if path == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading config file %s: %w", path, err)
	}
	return l.interpolate(data), true, nil
}

// Load deep-merges the base YAML layer with the optional per-impl/per-persona layer on
// top (per-impl keys win; nested maps merge key-wise; unmerged keys inherit the base or,
// if absent from both layers, whatever value `into` already carries -- callers pre-set
// `into` with the compiled-in defaults before calling Load, giving the full D7
// precedence chain: compiled-in defaults < base YAML < per-impl YAML < env, the last
// step applied afterwards via Env).
//
// found reports whether either layer existed on disk; personas decide whether
// found==false is fatal (the controller's file-only implementations map makes it so,
// the tf runner runs on defaults+env alone).
func (l *Loader) Load(basePath, perImplPath string, into any) (found bool, err error) {
	baseRaw, baseFound, err := l.readLayer(basePath)
	if err != nil {
		return false, err
	}
	implRaw, implFound, err := l.readLayer(perImplPath)
	if err != nil {
		return false, err
	}
	if !baseFound && !implFound {
		return false, nil
	}

	base, err := unmarshalToMap(baseRaw)
	if err != nil {
		return false, fmt.Errorf("parsing config file %s: %w", basePath, err)
	}
	impl, err := unmarshalToMap(implRaw)
	if err != nil {
		return false, fmt.Errorf("parsing config file %s: %w", perImplPath, err)
	}

	merged := deepMerge(base, impl)

	if err := decodeMap(merged, into); err != nil {
		return false, fmt.Errorf("decoding merged config: %w", err)
	}
	return true, nil
}
