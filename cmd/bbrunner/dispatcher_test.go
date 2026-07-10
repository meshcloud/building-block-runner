package main

import "testing"

func TestDetectDispatcherKind(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want dispatcherKind
	}{
		{"explicit k8sjob override wins over in-cluster", map[string]string{"RUNNER_DISPATCHER": "k8sjob", "KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherK8sJob},
		{"explicit inprocess override wins in-cluster", map[string]string{"RUNNER_DISPATCHER": "inprocess", "KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherInProcess},
		{"auto in-cluster => k8sjob", map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherK8sJob},
		{"auto out-of-cluster => inprocess", map[string]string{}, dispatcherInProcess},
		{"unrecognized override falls back to auto (in-cluster)", map[string]string{"RUNNER_DISPATCHER": "bogus", "KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherK8sJob},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			if got := detectDispatcherKind(getenv); got != tt.want {
				t.Errorf("detectDispatcherKind() = %q, want %q", got, tt.want)
			}
		})
	}
}
