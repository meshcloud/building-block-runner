//go:build k8s || !lean

package k8sjob

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// fakeJobMetrics is a no-op JobMetrics test double: this file's tests assert on the
// resulting Kubernetes objects and returned errors, not on metric side effects (those are
// covered directly against dispatch.MetricsCollector, internal/dispatch/metrics_test.go).
type fakeJobMetrics struct {
	decryptionErrors int
}

func (f *fakeJobMetrics) ObserveJobCreationDuration(string, float64)    {}
func (f *fakeJobMetrics) IncJobCreationError(string, string)            {}
func (f *fakeJobMetrics) IncJobsCreated(string)                         {}
func (f *fakeJobMetrics) IncServiceAccountCreationError(string, string) {}
func (f *fakeJobMetrics) IncServiceAccountsCreated(string)              {}
func (f *fakeJobMetrics) IncDecryptionError(string)                     { f.decryptionErrors++ }

// buildManualClaimedRun builds a ClaimedRun for a MANUAL run (no crypto needed -- MANUAL has
// no secrets to decrypt, decryption_test.go's TestDecryptRunDetails_ManualImplementation
// pins the same nil-crypto path).
func buildManualClaimedRun(t *testing.T) dispatch.ClaimedRun {
	t.Helper()
	const runId, bbWorkspace, bbdUuid, bbdWorkspace = "run-1", "ws-a", "bbd-1", "ws-bbd"
	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: runId},
		Spec: meshapi.RunSpecDTO{
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-uuid",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					WorkspaceIdentifier: bbWorkspace,
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: bbdUuid,
				Spec: meshapi.DefinitionDetailsSpecDTO{
					WorkspaceIdentifier: bbdWorkspace,
					Implementation:      json.RawMessage(`{"type":"MANUAL"}`),
				},
			},
		},
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("failed to marshal run details: %v", err)
	}
	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(runId),
		Type:    meshapi.RunnerTypeManual,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}
}

func newTestDispatcher(cfg config.K8sJobConfig) (*KubernetesJobDispatcher, *fake.Clientset, *fakeJobMetrics) {
	clientset := fake.NewClientset()
	metrics := &fakeJobMetrics{}
	d := NewKubernetesJobDispatcherWithClient(clientset, cfg, "runner-uuid", "https://api.example.com", nil, metrics, nil)
	return d, clientset, metrics
}

func TestDispatch_CreatesServiceAccountSecretAndJob(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace: "test-ns",
		Implementations: map[string]config.JobSpecTemplate{
			"MANUAL": {Image: "manual-runner:latest"},
		},
	}
	d, clientset, _ := newTestDispatcher(cfg)
	run := buildManualClaimedRun(t)

	if err := d.Dispatch(run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, err := clientset.BatchV1().Jobs("test-ns").Get(context.TODO(), "runner-run-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected job to be created: %v", err)
	}

	// BackoffLimit 0: a runner Job is a single, potentially state-mutating terraform run, so k8s
	// must never re-run it on pod failure/deletion (that would repeat the APPLY/DESTROY); the runner
	// reports terminal ABORTED itself on SIGTERM instead.
	if got := *job.Spec.BackoffLimit; got != 0 {
		t.Errorf("expected BackoffLimit 0 (no k8s rerun), got %d", got)
	}
	if got := *job.Spec.TTLSecondsAfterFinished; got != 120 {
		t.Errorf("expected TTLSecondsAfterFinished 120, got %d", got)
	}
	if got := job.Labels["meshcloud.io/runner-id"]; got != "runner-uuid" {
		t.Errorf("expected runner-id label %q, got %q", "runner-uuid", got)
	}
	if got := job.Labels["meshcloud.io/runner-type"]; got != "MANUAL" {
		t.Errorf("expected runner-type label %q, got %q", "MANUAL", got)
	}

	wantServiceAccount := "workspace.ws-bbd.buildingblockdefinition.bbd-1"
	if job.Spec.Template.Spec.ServiceAccountName != wantServiceAccount {
		t.Errorf("expected service account %q, got %q", wantServiceAccount, job.Spec.Template.Spec.ServiceAccountName)
	}

	if _, err := clientset.CoreV1().ServiceAccounts("test-ns").Get(context.TODO(), wantServiceAccount, metav1.GetOptions{}); err != nil {
		t.Errorf("expected service account to be created: %v", err)
	}

	secret, err := clientset.CoreV1().Secrets("test-ns").Get(context.TODO(), "run-json-run-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected run json secret to be created: %v", err)
	}
	var decoded meshapi.RunDetailsDTO
	if err := json.Unmarshal(secret.Data["run.json"], &decoded); err != nil {
		t.Fatalf("expected secret run.json to be valid JSON: %v", err)
	}
	if decoded.Metadata.Uuid != "run-1" {
		t.Errorf("expected the secret to carry the run's own JSON, got %+v", decoded)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != job.Name {
		t.Errorf("expected the secret to have an owner reference to the job, got %+v", secret.OwnerReferences)
	}

	container := job.Spec.Template.Spec.Containers[0]
	env := map[string]string{}
	for _, e := range container.Env {
		env[e.Name] = e.Value
	}
	if env["RUN_JSON_FILE_PATH"] != "/var/run/secrets/meshstack/run.json" {
		t.Errorf("unexpected RUN_JSON_FILE_PATH: %q", env["RUN_JSON_FILE_PATH"])
	}
	if env["RUNNER_UUID"] != "runner-uuid" {
		t.Errorf("unexpected RUNNER_UUID: %q", env["RUNNER_UUID"])
	}
	if env["RUNNER_API_URL"] != "https://api.example.com" {
		t.Errorf("unexpected RUNNER_API_URL: %q", env["RUNNER_API_URL"])
	}
	if _, ok := env["EXECUTION_MODE"]; ok {
		t.Error("EXECUTION_MODE must never be injected by the dispatcher (D9: it is deployment config, not code)")
	}
}

func TestDispatch_JobAlreadyExists_IsANoop(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:       "test-ns",
		Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}},
	}
	d, clientset, _ := newTestDispatcher(cfg)
	existing := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "runner-run-1", Namespace: "test-ns"}}
	if _, err := clientset.BatchV1().Jobs("test-ns").Create(context.TODO(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to seed existing job: %v", err)
	}

	run := buildManualClaimedRun(t)
	if err := d.Dispatch(run); err != nil {
		t.Fatalf("unexpected error when the job already exists: %v", err)
	}

	if _, err := clientset.CoreV1().Secrets("test-ns").Get(context.TODO(), "run-json-run-1", metav1.GetOptions{}); err == nil {
		t.Error("expected no secret to be created when the job already exists")
	}
}

func TestDispatch_UnhandledType_ReturnsUnhandledTypeError(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:       "test-ns",
		Implementations: map[string]config.JobSpecTemplate{"TERRAFORM": {Image: "tf:latest"}},
	}
	d, _, _ := newTestDispatcher(cfg)
	run := buildManualClaimedRun(t) // Type = MANUAL, not configured

	err := d.Dispatch(run)
	var unhandled *dispatch.UnhandledTypeError
	if !errors.As(err, &unhandled) {
		t.Fatalf("expected *dispatch.UnhandledTypeError, got %v (%T)", err, err)
	}
	if unhandled.Message != "no implementation handler configured for type 'MANUAL'" {
		t.Errorf("unexpected frozen message: %q", unhandled.Message)
	}
}

// TestDispatch_DecryptFailure_ReportsActionableMessage is the flipped pin (was
// TestDispatch_DecryptFailure_IsSilentDispatchFailure): a decrypt failure is no longer a
// silent dispatch failure. It now returns an ordinary reportable error (Loop reports its
// Error() text as the FAILED status) carrying actionable key-mismatch
// guidance, while still incrementing the decryption-error metric.
func TestDispatch_DecryptFailure_ReportsActionableMessage(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:       "test-ns",
		Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}},
	}
	d, _, metrics := newTestDispatcher(cfg)
	run := buildManualClaimedRun(t)
	run.RawJson = "not-valid-base64!!!"

	err := d.Dispatch(run)
	if err == nil {
		t.Fatal("expected a decrypt failure error, got nil")
	}
	var unhandled *dispatch.UnhandledTypeError
	if errors.As(err, &unhandled) {
		t.Fatalf("decrypt failure must be a plain reportable error, not UnhandledTypeError: %v", err)
	}
	if !strings.Contains(err.Error(), "key mismatch") {
		t.Errorf("expected actionable key-mismatch guidance in the reported message, got %q", err.Error())
	}
	if metrics.decryptionErrors != 1 {
		t.Errorf("expected the decryption-error metric to be incremented once, got %d", metrics.decryptionErrors)
	}
}

func TestDispatch_RunTooLarge_ReturnsFrozenActionableMessage(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:       "test-ns",
		Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}},
	}
	d, _, _ := newTestDispatcher(cfg)
	run := buildManualClaimedRun(t)
	// The size guard runs after decryption (order: decrypt -> template -> size -> Job), so
	// the payload must still decrypt/parse cleanly -- pad a harmless field instead of
	// corrupting RawJson, to push the decoded size over EffectiveMaxRunJsonSize.
	run.Details.Spec.BuildingBlock.Spec.DisplayName = strings.Repeat("A", EffectiveMaxRunJsonSize+1024)
	raw, err := json.Marshal(run.Details)
	if err != nil {
		t.Fatalf("failed to marshal padded run details: %v", err)
	}
	run.RawJson = base64.StdEncoding.EncodeToString(raw)

	err = d.Dispatch(run)
	if err == nil {
		t.Fatal("expected an error for an oversized run")
	}
	want := "Run data is too large to be passed to the runner. The run data exceeds the Kubernetes secret size limit of 1MiB. Please reduce the size of the building block inputs."
	if err.Error() != want {
		t.Errorf("expected the frozen 1MiB message, got: %q", err.Error())
	}
}

func TestInFlight_CountsOnlyUnfinishedJobsForThisRunner(t *testing.T) {
	cfg := config.K8sJobConfig{Namespace: "test-ns"}
	d, clientset, _ := newTestDispatcher(cfg)

	jobs := []*batchv1.Job{
		{ // unfinished, this runner
			ObjectMeta: metav1.ObjectMeta{Name: "j1", Namespace: "test-ns", Labels: map[string]string{"meshcloud.io/runner-id": "runner-uuid"}},
		},
		{ // finished (Complete), this runner -- must not count
			ObjectMeta: metav1.ObjectMeta{Name: "j2", Namespace: "test-ns", Labels: map[string]string{"meshcloud.io/runner-id": "runner-uuid"}},
			Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			}},
		},
		{ // unfinished, a DIFFERENT runner -- must not count (label selector)
			ObjectMeta: metav1.ObjectMeta{Name: "j3", Namespace: "test-ns", Labels: map[string]string{"meshcloud.io/runner-id": "other-runner"}},
		},
		{ // failed, this runner -- must not count
			ObjectMeta: metav1.ObjectMeta{Name: "j4", Namespace: "test-ns", Labels: map[string]string{"meshcloud.io/runner-id": "runner-uuid"}},
			Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			}},
		},
	}
	for _, j := range jobs {
		if _, err := clientset.BatchV1().Jobs("test-ns").Create(context.TODO(), j, metav1.CreateOptions{}); err != nil {
			t.Fatalf("failed to seed job %s: %v", j.Name, err)
		}
	}

	got, err := d.InFlight()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Errorf("expected 1 in-flight job, got %d", got)
	}
}

func TestDispatch_AppliesTolerationsNodeSelectorAndImagePullSecrets(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:        "test-ns",
		ImagePullSecrets: []string{"regcred"},
		NodeSelector:     map[string]string{"pool": "spot"},
		Tolerations: []config.TolerationConfig{
			{Key: "spot", Operator: "Exists", Effect: "NoSchedule"},
		},
		Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}},
	}
	d, clientset, _ := newTestDispatcher(cfg)
	run := buildManualClaimedRun(t)

	if err := d.Dispatch(run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, err := clientset.BatchV1().Jobs("test-ns").Get(context.TODO(), "runner-run-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected job to be created: %v", err)
	}

	podSpec := job.Spec.Template.Spec
	if len(podSpec.ImagePullSecrets) != 1 || podSpec.ImagePullSecrets[0].Name != "regcred" {
		t.Errorf("expected imagePullSecrets [regcred], got %+v", podSpec.ImagePullSecrets)
	}
	if podSpec.NodeSelector["pool"] != "spot" {
		t.Errorf("expected nodeSelector pool=spot, got %+v", podSpec.NodeSelector)
	}
	if len(podSpec.Tolerations) != 1 || podSpec.Tolerations[0].Key != "spot" {
		t.Errorf("expected one 'spot' toleration, got %+v", podSpec.Tolerations)
	}
}
