// Package config is the shared config-loading mechanics for every runner type:
// two-YAML-layer deep-merge (shared base overlaid by a per-runner-type/per-impl file),
// ${VAR} interpolation inside those YAML layers, explicit env-var bindings (highest
// precedence), and a fail-fast guard against unrecognized legacy-prefixed env vars.
//
// Runner type config *structs* stay in their runner type packages (tf keeps its keys, the
// controller keeps ControllerConfig incl. the file-only implementations map) — this
// package owns only the mechanics and the genuinely shared Api section (api.go).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
)

// Loader deep-merges the two config YAML layers and tracks which environment variable
// names it has "consumed" — via ${VAR} interpolation inside a YAML layer or via an
// explicit Env/Path binding — so FailOnUnconsumedLegacyEnv can tell a recognized
// override apart from a Spring relaxed-binding holdover nothing here understands.
//
// A Loader is not safe for concurrent use; each runner type's startup constructs and uses
// exactly one, sequentially, which matches how config loading happens today.
type Loader struct {
	consumed map[string]bool
	// ignoredLegacyBlocks records which legacyIgnoredBlockKeys Load saw as top-level keys in
	// a config layer, so WarnIgnoredLegacyYAMLBlocks can warn about each one once.
	ignoredLegacyBlocks []string
}

// legacyIgnoredBlockKeys are the top-level YAML keys that only ever configured the
// Spring/JVM generation of these runners (Spring Boot's own logging and embedded-server
// settings, plus any spring.* property tree). The Go runners consume none of them, so a
// mounted config file that still carries one has no effect -- but yaml.Unmarshal silently
// drops unknown top-level keys (merge.go), so without an explicit check an operator gets no
// signal the block is inert. These are ignored-WITH-WARNING (docs/DEPRECATIONS.md §4),
// deliberately distinct from the BLOCKRUNNER_*-prefixed env fail-fast guard
// (FailOnUnconsumedLegacyEnv): a stray legacy env override must halt startup, but a leftover
// Spring yaml block is harmless and only warned.
var legacyIgnoredBlockKeys = []string{"logging", "server", "spring"}

// NewLoader returns a Loader ready for one runner type's startup sequence.
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
// Intentionally minimal ("${VAR} interpolation", not a default-value
// expression language) -- an unset VAR resolves to the empty string, exactly like an
// unset shell variable would.
var interpolationPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolate replaces every ${VAR} occurrence in raw with os.Getenv(VAR) and records
// VAR as consumed -- the declarative mapping in the YAML *is* the consumption, whether
// or not the operator happens to have that variable set (the base runner-config.yml
// maps legacy env vars onto struct keys declaratively).
func (l *Loader) interpolate(raw []byte) []byte {
	return interpolationPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := string(interpolationPattern.FindSubmatch(match)[1])
		l.markConsumed(name)
		return []byte(os.Getenv(name))
	})
}

// readLayer reads path (empty path = no layer configured for this type) and
// interpolates it. A missing file is tolerated (many runner types run on defaults+env
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

// Load deep-merges the base YAML layer with the optional per-impl/per-type layer on
// top (per-impl keys win; nested maps merge key-wise; unmerged keys inherit the base or,
// if absent from both layers, whatever value `into` already carries -- callers pre-set
// `into` with the compiled-in defaults before calling Load, giving the full
// precedence chain: compiled-in defaults < base YAML < per-impl YAML < env, the last
// step applied afterwards via Env).
//
// found reports whether either layer existed on disk; runner types decide whether
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
	l.recordIgnoredLegacyBlocks(merged)

	if err := decodeMap(merged, into); err != nil {
		return false, fmt.Errorf("decoding merged config: %w", err)
	}
	return true, nil
}

// recordIgnoredLegacyBlocks notes which legacyIgnoredBlockKeys appear as top-level keys in
// the merged config document (either layer), so WarnIgnoredLegacyYAMLBlocks can warn about
// each one once. yaml.v2 decodes YAML mapping keys as `string` inside a
// map[interface{}]interface{}, so a plain string index matches.
func (l *Loader) recordIgnoredLegacyBlocks(merged map[interface{}]interface{}) {
	for _, key := range legacyIgnoredBlockKeys {
		if _, present := merged[key]; present {
			l.ignoredLegacyBlocks = append(l.ignoredLegacyBlocks, key)
		}
	}
}

// WarnIgnoredLegacyYAMLBlocks logs one warn-and-ignore line per legacy Spring/JVM top-level
// block (logging/server/spring) that Load found in a config layer. These blocks are ignored,
// not consumed, so the wording says "ignoring" and points at docs/DEPRECATIONS.md rather than
// naming a canonical replacement (there is none) -- matching the warn-and-ignore style the
// ported runner types already use for an inapplicable blockrunner.* key. It is a warning, not a
// failure: unlike a stray BLOCKRUNNER_* env var (FailOnUnconsumedLegacyEnv, a hard error), a
// leftover Spring yaml block cannot silently boot the runner on wrong defaults. Call it once,
// after Load, during a type's startup.
func (l *Loader) WarnIgnoredLegacyYAMLBlocks(log *slog.Logger) {
	for _, block := range l.ignoredLegacyBlocks {
		log.Warn("ignoring unsupported legacy config block '"+block+":'; it configured only the Spring/JVM runner generation and has no effect on this Go runner -- see "+DeprecationDoc,
			"block", block)
	}
}
