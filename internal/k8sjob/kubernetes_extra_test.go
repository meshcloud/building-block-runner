//go:build k8s || !lean

package k8sjob

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func TestRunTooLargeError_Error(t *testing.T) {
	err := &RunTooLargeError{RunId: "run-1", DataSize: 2000000, MaxSize: EffectiveMaxRunJsonSize}
	got := err.Error()
	if got == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestDispatch_CustomCommandArgsResourcesAndExtraVolumes(t *testing.T) {
	sizeLimit := "1Gi"
	cfg := config.K8sJobConfig{
		Namespace: "test-ns",
		Implementations: map[string]config.JobSpecTemplate{
			"MANUAL": {
				Image:   "manual-runner:latest",
				Command: []string{"/bin/custom-entrypoint"},
				Args:    []string{"--flag"},
				Env:     map[string]string{"CUSTOM_ENV": "value"},
				Resources: config.ResourcesConfig{
					Requests: config.ResourceSpec{Cpu: "250m", Memory: "512Mi"},
					Limits:   config.ResourceSpec{Cpu: "500m", Memory: "2Gi"},
				},
				ExtraVolumes: []config.ExtraVolume{
					{Name: "trusted-certs", ConfigMap: &config.ConfigMapVolumeSpec{Name: "certs-cm"}},
					{Name: "extra-secret", Secret: &config.SecretVolumeSpec{SecretName: "extra-secret-name"}},
					{Name: "scratch", EmptyDir: &config.EmptyDirVolumeSpec{SizeLimit: sizeLimit}},
				},
				ExtraVolumeMounts: []config.ExtraVolumeMount{
					{Name: "trusted-certs", MountPath: "/etc/ssl/certs/extra", ReadOnly: true},
				},
			},
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

	container := job.Spec.Template.Spec.Containers[0]
	if len(container.Command) != 1 || container.Command[0] != "/bin/custom-entrypoint" {
		t.Errorf("expected custom command, got %+v", container.Command)
	}
	if len(container.Args) != 1 || container.Args[0] != "--flag" {
		t.Errorf("expected custom args, got %+v", container.Args)
	}

	foundCustomEnv := false
	for _, e := range container.Env {
		if e.Name == "CUSTOM_ENV" && e.Value == "value" {
			foundCustomEnv = true
		}
	}
	if !foundCustomEnv {
		t.Errorf("expected CUSTOM_ENV to be present, got %+v", container.Env)
	}

	if got := container.Resources.Requests.Cpu().String(); got != "250m" {
		t.Errorf("expected cpu request 250m, got %s", got)
	}
	if got := container.Resources.Limits.Cpu().String(); got != "500m" {
		t.Errorf("expected cpu limit 500m, got %s", got)
	}
	if got := container.Resources.Limits.Memory().String(); got != "2Gi" {
		t.Errorf("expected memory limit 2Gi, got %s", got)
	}

	volumesByName := map[string]corev1.Volume{}
	for _, v := range job.Spec.Template.Spec.Volumes {
		volumesByName[v.Name] = v
	}
	if v, ok := volumesByName["trusted-certs"]; !ok || v.ConfigMap == nil || v.ConfigMap.Name != "certs-cm" {
		t.Errorf("expected trusted-certs ConfigMap volume, got %+v", v)
	}
	if v, ok := volumesByName["extra-secret"]; !ok || v.Secret == nil || v.Secret.SecretName != "extra-secret-name" {
		t.Errorf("expected extra-secret Secret volume, got %+v", v)
	}
	if v, ok := volumesByName["scratch"]; !ok || v.EmptyDir == nil || v.EmptyDir.SizeLimit == nil || v.EmptyDir.SizeLimit.String() != "1Gi" {
		t.Errorf("expected scratch EmptyDir volume with 1Gi size limit, got %+v", v)
	}

	foundExtraMount := false
	for _, m := range container.VolumeMounts {
		if m.Name == "trusted-certs" && m.MountPath == "/etc/ssl/certs/extra" && m.ReadOnly {
			foundExtraMount = true
		}
	}
	if !foundExtraMount {
		t.Errorf("expected the extra volume mount, got %+v", container.VolumeMounts)
	}
}

func TestDispatch_ServiceAccountAndSecretAlreadyExist_AreNoops(t *testing.T) {
	cfg := config.K8sJobConfig{
		Namespace:       "test-ns",
		Implementations: map[string]config.JobSpecTemplate{"MANUAL": {Image: "manual-runner:latest"}},
	}
	d, clientset, _ := newTestDispatcher(cfg)

	// Pre-seed the ServiceAccount and Secret this run would otherwise create, exercising the
	// "already exists, skip creation" branches.
	existingSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "workspace.ws-bbd.buildingblockdefinition.bbd-1", Namespace: "test-ns"}}
	if _, err := clientset.CoreV1().ServiceAccounts("test-ns").Create(context.TODO(), existingSA, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to seed service account: %v", err)
	}
	existingSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "run-json-run-1", Namespace: "test-ns"}}
	if _, err := clientset.CoreV1().Secrets("test-ns").Create(context.TODO(), existingSecret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to seed secret: %v", err)
	}

	run := buildManualClaimedRun(t)
	if err := d.Dispatch(run); err != nil {
		t.Fatalf("unexpected error when SA/secret already exist: %v", err)
	}

	if _, err := clientset.BatchV1().Jobs("test-ns").Get(context.TODO(), "runner-run-1", metav1.GetOptions{}); err != nil {
		t.Errorf("expected the job to still be created: %v", err)
	}
}

// Ensures UnhandledTypeError satisfies the dispatch.Dispatcher contract's documented error
// shape even when wrapped -- a lightweight compile-time-adjacent smoke test for the type
// plumbing between k8sjob and dispatch.
var _ error = (*dispatch.UnhandledTypeError)(nil)
var _ = meshapi.RunnerTypeManual
