//go:build k8s || !lean

package k8sjob

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubetesting "k8s.io/client-go/testing"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// This file exercises the error paths a happy-path fake clientset never reaches: k8s API
// calls failing partway through Job/Secret/ServiceAccount creation, and the capacity-check
// list call failing. Each uses a k8s.io/client-go/testing reactor to inject the failure on
// the real fake clientset (the same seam kubernetes_test.go's happy paths already use).

func TestDispatch_ServiceAccountCreationError_IsPropagated(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns", Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}}}
	d, clientset, metrics := newTestDispatcher(cfg)
	clientset.PrependReactor("create", "serviceaccounts", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	run := buildManualClaimedRun(t)
	if err := d.Dispatch(run); err == nil {
		t.Fatal("expected an error when service account creation fails")
	}
	if metrics.decryptionErrors != 0 {
		t.Errorf("service account failure must not be counted as a decryption error, got %d", metrics.decryptionErrors)
	}
}

func TestDispatch_RunJsonSecretCreationError_IsPropagated(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns", Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}}}
	d, clientset, _ := newTestDispatcher(cfg)
	clientset.PrependReactor("create", "secrets", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("quota exceeded")
	})

	run := buildManualClaimedRun(t)
	if err := d.Dispatch(run); err == nil {
		t.Fatal("expected an error when the run json secret cannot be created")
	}
}

func TestDispatch_JobCreationError_CleansUpSecret(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns", Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}}}
	d, clientset, _ := newTestDispatcher(cfg)
	clientset.PrependReactor("create", "jobs", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("jobs quota exceeded")
	})

	run := buildManualClaimedRun(t)
	err := d.Dispatch(run)
	if err == nil {
		t.Fatal("expected an error when job creation fails")
	}
	if err.Error() != "Failed to create job for run: failed to create job: jobs quota exceeded" {
		t.Errorf("unexpected error text: %q", err.Error())
	}

	if _, getErr := clientset.CoreV1().Secrets("test-ns").Get(context.TODO(), "run-json-run-1", metav1.GetOptions{}); getErr == nil {
		t.Error("expected the run json secret to be cleaned up after a failed job creation")
	}
}

func TestDispatch_SecretOwnerReferenceUpdateFailure_IsLoggedNotFatal(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns", Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}}}
	d, clientset, _ := newTestDispatcher(cfg)
	clientset.PrependReactor("update", "secrets", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("conflict")
	})

	run := buildManualClaimedRun(t)
	// A failure to set the owner reference is a warning, not a Dispatch error (the job/secret
	// still exist and the run still proceeds) -- matches the pre-existing controller behavior.
	if err := d.Dispatch(run); err != nil {
		t.Fatalf("expected owner-reference failure to be non-fatal, got: %v", err)
	}
}

func TestInFlight_ListError_IsPropagated(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns"}
	d, clientset, _ := newTestDispatcher(cfg)
	clientset.PrependReactor("list", "jobs", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api unavailable")
	})

	if _, err := d.InFlight(); err == nil {
		t.Fatal("expected an error when listing jobs fails")
	}
}
