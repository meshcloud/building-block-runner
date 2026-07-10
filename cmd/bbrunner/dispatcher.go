package main

import "os"

// dispatcherKind selects which Dispatcher the run-controller/superset drives its dispatch.Loop
// with (PLAN_DETAIL_05 §A1/D1-D2): the in-cluster run-controller hands runs to Kubernetes Jobs
// (k8sjob), while a run-controller image running OUTSIDE a cluster (the meshfed-release
// multiplexing-block-runner replacement) runs every handler in-process (InProcess superset).
type dispatcherKind string

const (
	dispatcherK8sJob    dispatcherKind = "k8sjob"
	dispatcherInProcess dispatcherKind = "inprocess"

	// envRunnerDispatcher overrides the auto-detected dispatcher. Accepted values are the two
	// dispatcherKind constants; any other value falls back to auto-detection.
	envRunnerDispatcher = "RUNNER_DISPATCHER"
	// envKubernetesServiceHost is set by the kubelet inside every pod; its presence is the
	// canonical "am I running in-cluster?" signal (equivalent to rest.InClusterConfig
	// succeeding, without importing client-go here just to probe).
	envKubernetesServiceHost = "KUBERNETES_SERVICE_HOST"
)

// detectDispatcherKind resolves the dispatcher to use: an explicit RUNNER_DISPATCHER override
// wins (k8sjob|inprocess); otherwise it auto-detects (in-cluster => k8sjob, else => inprocess).
// getenv is injected so the detection is unit-testable without mutating the process environment.
func detectDispatcherKind(getenv func(string) string) dispatcherKind {
	switch getenv(envRunnerDispatcher) {
	case string(dispatcherK8sJob):
		return dispatcherK8sJob
	case string(dispatcherInProcess):
		return dispatcherInProcess
	}
	if getenv(envKubernetesServiceHost) != "" {
		return dispatcherK8sJob
	}
	return dispatcherInProcess
}

// resolveDispatcherKind is detectDispatcherKind over the real process environment.
func resolveDispatcherKind() dispatcherKind {
	return detectDispatcherKind(os.Getenv)
}
