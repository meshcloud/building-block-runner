package config

import (
	"log/slog"
	"os"
	"strings"
)

const (
	// envExecutionMode is the Go-native single-run trigger, shared with the tf type
	// (cmd/tf: EXECUTION_MODE=single-run).
	envExecutionMode = "EXECUTION_MODE"
	// envSpringProfiles is the deployed operator contract for the five Kotlin-port runner types:
	// their k8s Job templates bake SPRING_PROFILES_ACTIVE=kubernetes
	// (run-controller/runner-config.yml), so the Go images MUST honor it as a single-run
	// trigger or every existing controller deployment breaks on image update.
	// Neither variable is ever *required* — rollback to the JVM images
	// stays symmetric.
	envSpringProfiles = "SPRING_PROFILES_ACTIVE"
	// executionModeSingleRun is the EXECUTION_MODE value that selects single-run mode.
	executionModeSingleRun = "single-run"
	// springProfileKubernetes is the SPRING_PROFILES_ACTIVE list member that selects
	// single-run mode.
	springProfileKubernetes = "kubernetes"
)

// SingleRunMode reports whether this type should run in single-run (one run from a
// mounted file, then exit) rather than polling mode. It is true when EXECUTION_MODE is
// "single-run" OR SPRING_PROFILES_ACTIVE contains "kubernetes" as a comma-separated list
// member (Spring profile-list semantics: "kubernetes,extra" still activates). The Spring
// path is deprecation-logged once; EXECUTION_MODE is the preferred spelling going forward.
//
// This is the shared helper all five runner types call; tf keeps its own
// EXECUTION_MODE-only check unchanged (out-of-scope).
func SingleRunMode(log *slog.Logger) bool {
	if os.Getenv(envExecutionMode) == executionModeSingleRun {
		return true
	}
	for _, profile := range strings.Split(os.Getenv(envSpringProfiles), ",") {
		if strings.TrimSpace(profile) == springProfileKubernetes {
			WarnDeprecated(log, envSpringProfiles+"="+springProfileKubernetes, envExecutionMode+"="+executionModeSingleRun)
			return true
		}
	}
	return false
}
