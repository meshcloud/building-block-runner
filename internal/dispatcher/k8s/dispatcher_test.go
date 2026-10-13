//go:build k8s

package k8s

import (
	"context"
	"io"
	"log"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const namespace = "runners"

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

func testConfig(templates config.JobSpecTemplates) config.Config {
	var cfg config.Config
	cfg.Uuid = runnerUuid
	cfg.Api.Url = xurl.MustParse("http://meshstack.example.com")
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeAll}
	cfg.Dispatcher.Timeout = xtime.Minutes(45)
	cfg.K8s = &config.K8s{Namespace: namespace, JobSpecTemplates: templates}
	return cfg
}

func runnerTemplate(container corev1.Container, podSpec corev1.PodSpec) config.JobSpecTemplate {
	container.Name = runnerContainerName
	podSpec.Containers = append(podSpec.Containers, container)
	return config.JobSpecTemplate{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: podSpec}}}
}

func newTfRun() dispatcher.Run {
	run := dispatcher.Run{Metadata: meshapi.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "a-run-token"
	run.Spec.Definition.Uuid = "f3228cf4-acde-4ff3-a0a5-1720b69d5bcd"
	run.Spec.Definition.Spec.WorkspaceIdentifier = "my-workspace"
	run.Spec.Definition.Spec.Implementation = []byte(`{"type":"TERRAFORM"}`)
	return run
}

func startTestDispatcher(t *testing.T, ctx context.Context, cfg config.Config, jobs ...*batchv1.Job) (*Dispatcher, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewClientset()
	for _, job := range jobs {
		_, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	d, err := start(ctx, cfg, clientset, "", log.New(io.Discard, "", 0))
	require.NoError(t, err)
	return d, clientset
}

func Test_Start_MergesTheJobSkeletonOverTheTemplate(t *testing.T) {
	template := runnerTemplate(corev1.Container{
		Image:           "ghcr.io/meshcloud/tf-block-runner:main",
		Env:             []corev1.EnvVar{{Name: "EXECUTION_MODE", Value: "single-run"}, {Name: "RUNNER_CLAIM_RUN_YML", Value: "/elsewhere"}},
		SecurityContext: &corev1.SecurityContext{Privileged: new(true)},
		Resources:       corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}},
	}, corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyOnFailure,
		ServiceAccountName: "privileged",
		Volumes:            []corev1.Volume{{Name: runVolumeName}, {Name: "trusted-certs"}},
	})
	template.Labels = map[string]string{labelRunnerId: "another-runner", "team": "platform"}
	template.Spec.BackoffLimit = new(int32(3))
	d, clientset := startTestDispatcher(t, t.Context(), testConfig(config.JobSpecTemplates{config.RunnerTypeTf: template}))
	run := newTfRun()

	require.NoError(t, d.Start(t.Context(), run))

	job, err := clientset.BatchV1().Jobs(namespace).Get(t.Context(), "runner-"+run.Metadata.Uuid.String(), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, runnerUuid.String(), job.Labels[labelRunnerId])
	assert.Equal(t, "platform", job.Labels["team"])
	assert.Equal(t, "TERRAFORM", job.Labels[labelRunnerType])
	assert.Equal(t, int64(45*60), *job.Spec.ActiveDeadlineSeconds)
	assert.Equal(t, int32(3), *job.Spec.BackoffLimit)
	assert.Equal(t, int32(defaultTtlSeconds), *job.Spec.TTLSecondsAfterFinished)

	pod := job.Spec.Template.Spec
	assert.Equal(t, corev1.RestartPolicyNever, pod.RestartPolicy)
	assert.Equal(t, "workspace.my-workspace.buildingblockdefinition.f3228cf4-acde-4ff3-a0a5-1720b69d5bcd", pod.ServiceAccountName)
	assert.Equal(t, []string{"trusted-certs", runVolumeName, "service-account-token-azure", "service-account-token-gcp", "service-account-token-aws"}, names(pod.Volumes, volumeName))
	assert.Equal(t, "run-json-"+run.Metadata.Uuid.String(), pod.Volumes[1].Secret.SecretName)

	container := pod.Containers[0]
	assert.Equal(t, restrictedSecurityContext(), container.SecurityContext)
	env := map[string]string{}
	for _, envVar := range container.Env {
		env[envVar.Name] = envVar.Value
	}
	assert.Equal(t, "single-run", env["EXECUTION_MODE"])
	assert.Equal(t, "/var/run/secrets/meshstack/run.json", env["RUNNER_CLAIM_RUN_YML"])
	assert.Equal(t, "/var/run/secrets/meshstack/run.json", env["RUN_JSON_FILE_PATH"])
	assert.NotContains(t, env, "MESHSTACK_API_TOKEN")
	assert.Equal(t, "tf", env["RUNNER_DISPATCHER_FOR_TYPES"])
	assert.Equal(t, resource.MustParse("2Gi"), container.Resources.Limits[corev1.ResourceMemory])
	assert.Equal(t, resource.MustParse("256Mi"), container.Resources.Requests[corev1.ResourceMemory])
}

func Test_Start_StoresTheRunInTheSecretOfTheJob(t *testing.T) {
	d, clientset := startTestDispatcher(t, t.Context(), testConfig(config.JobSpecTemplates{config.RunnerTypeAll: runnerTemplate(corev1.Container{Image: "runner"}, corev1.PodSpec{})}))
	run := newTfRun()

	require.NoError(t, d.Start(t.Context(), run))

	secret, err := clientset.CoreV1().Secrets(namespace).Get(t.Context(), "run-json-"+run.Metadata.Uuid.String(), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Job", secret.OwnerReferences[0].Kind)
	assert.Equal(t, []string{runFile}, slices.Collect(maps.Keys(secret.Data)))
	assert.Contains(t, string(secret.Data[runFile]), `"runToken":"a-run-token"`)
	read, err := d.ReadRun(t.Context(), run.Metadata.Uuid)
	require.NoError(t, err)
	assert.Equal(t, run.Metadata.Uuid, read.Metadata.Uuid)
	assert.Equal(t, "a-run-token", read.Spec.RunToken)
}

func Test_Start_FailsForARunTooLargeForASecret(t *testing.T) {
	d, _ := startTestDispatcher(t, t.Context(), testConfig(config.JobSpecTemplates{config.RunnerTypeAll: runnerTemplate(corev1.Container{Image: "runner"}, corev1.PodSpec{})}))
	run := newTfRun()
	run.Spec.BuildingBlock.Spec.Inputs = []meshapi.MeshRunnerRunInput{{Key: "large", Value: []byte(`"` + strings.Repeat("x", 1<<20) + `"`)}}

	err := d.Start(t.Context(), run)

	assert.ErrorIs(t, err, dispatcher.ErrRunTooLarge)
}

func names[T any](items []T, name func(T) string) []string {
	var result []string
	for _, item := range items {
		result = append(result, name(item))
	}
	return result
}

func runningJob(runUuid uuid.UUID) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      "runner-" + runUuid.String(),
		Namespace: namespace,
		Labels:    map[string]string{labelRunnerId: runnerUuid.String(), labelRunId: runUuid.String()},
	}}
}

func withCondition(job *batchv1.Job, conditionType batchv1.JobConditionType, reason string) *batchv1.Job {
	job = job.DeepCopy()
	job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{Type: conditionType, Status: corev1.ConditionTrue, Reason: reason, Message: "details"})
	return job
}

func receive(t *testing.T, d *Dispatcher) dispatcher.RunStatus {
	t.Helper()
	select {
	case status := <-d.Status():
		return status
	case <-time.After(5 * time.Second):
		t.Fatal("no status")
		return dispatcher.RunStatus{}
	}
}

func assertNoStatus(t *testing.T, d *Dispatcher) {
	t.Helper()
	select {
	case status := <-d.Status():
		t.Fatalf("unexpected status %+v", status)
	case <-time.After(100 * time.Millisecond):
	}
}

func Test_Status_ReportsTheRunningJobsFirstAndFailedJobsOfAPreviousProcess(t *testing.T) {
	running, failed, reported, succeeded := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	reportedJob := withCondition(runningJob(reported), batchv1.JobFailed, "BackoffLimitExceeded")
	reportedJob.Annotations = map[string]string{annotationFailureReported: "true"}
	d, clientset := startTestDispatcher(t, t.Context(), testConfig(nil),
		runningJob(running),
		withCondition(runningJob(failed), batchv1.JobFailed, "DeadlineExceeded"),
		reportedJob,
		withCondition(runningJob(succeeded), batchv1.JobComplete, ""),
	)

	assert.Equal(t, dispatcher.RunStatus{RunUuid: running}, receive(t, d))
	failure := receive(t, d)
	assert.Equal(t, failed, failure.RunUuid)
	assert.True(t, failure.Ended)
	assert.EqualError(t, failure.Err, "the Kubernetes Job runner-"+failed.String()+" failed: DeadlineExceeded: details")
	assertNoStatus(t, d)

	assert.Eventually(t, func() bool {
		job, err := clientset.BatchV1().Jobs(namespace).Get(t.Context(), "runner-"+failed.String(), metav1.GetOptions{})
		return err == nil && job.Annotations[annotationFailureReported] == "true"
	}, 5*time.Second, 10*time.Millisecond)
}

func Test_Status_ReportsAJobThatEnds(t *testing.T) {
	succeeding, failing := uuid.New(), uuid.New()
	d, clientset := startTestDispatcher(t, t.Context(), testConfig(nil), runningJob(succeeding), runningJob(failing))
	jobs := clientset.BatchV1().Jobs(namespace)
	receive(t, d)
	receive(t, d)

	_, err := jobs.UpdateStatus(t.Context(), withCondition(runningJob(succeeding), batchv1.JobComplete, ""), metav1.UpdateOptions{})
	require.NoError(t, err)
	assert.Equal(t, dispatcher.RunStatus{RunUuid: succeeding, Ended: true}, receive(t, d))

	_, err = jobs.UpdateStatus(t.Context(), withCondition(runningJob(failing), batchv1.JobFailed, "BackoffLimitExceeded"), metav1.UpdateOptions{})
	require.NoError(t, err)
	failure := receive(t, d)
	assert.Equal(t, failing, failure.RunUuid)
	assert.ErrorContains(t, failure.Err, "BackoffLimitExceeded")
	assertNoStatus(t, d)
}

func Test_Status_ClosesWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	d, _ := startTestDispatcher(t, ctx, testConfig(nil))

	cancel()

	_, open := <-d.Status()
	assert.False(t, open)
}

func Test_WorkloadIdentity_MatchesTheServiceAccountAndTokenMounts(t *testing.T) {
	wif := newWorkloadIdentity(namespace, "https://issuer.example.com")

	assert.Equal(t, "system:serviceaccount:runners:workspace.{{ workspaceIdentifier }}.buildingblockdefinition.{{ buildingBlockDefinitionUuid }}", wif.SubjectTemplate)
	assert.Equal(t, &meshapi.MeshRunnerRegistrationWifProvider{Audience: "gcp-workload-identity-provider:runners", TokenPath: "/var/run/secrets/workload-identity/gcp/token"}, wif.Gcp)
	assert.Nil(t, newWorkloadIdentity(namespace, ""))
}
