package config

import "log/slog"

// DeprecationDoc is the operator-facing home of the accumulated alias inventory and its
// removal timeline -- every deprecation warning links here
// instead of leaving the operator to reconstruct the replacement from source comments.
const DeprecationDoc = "docs/DEPRECATIONS.md"

// WarnDeprecated logs one uniform warning line for a legacy config surface (env var or
// yaml key) that is still being honored in place of its canonical replacement. Every
// deprecation warning in this repo -- inside this package (Path/ManagementPort/
// BlockRunnerCompat/SingleRunMode) and in the ported runner types' loadconfig files -- goes
// through this single helper so wording never drifts between call sites (before this, each
// call site had invented its own phrasing).
//
// old and replacement are short human-readable descriptions of the deprecated surface and
// its replacement (e.g. an env var name, or a prose description when the replacement isn't
// a single named knob, such as VERSION's "the compiled-in build version").
func WarnDeprecated(log *slog.Logger, old, replacement string) {
	log.Warn("deprecated: "+old+" is deprecated, use "+replacement+" instead -- see "+DeprecationDoc,
		"deprecated", old, "replacement", replacement)
}
