//go:build k8s

package k8s

import (
	"context"
	"errors"
	"io"
	"log"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
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
	run := dispatcher.Run{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "a-run-token"
	run.Spec.Definition.Uuid = "f3228cf4-acde-4ff3-a0a5-1720b69d5bcd"
	run.Spec.Definition.Spec.WorkspaceIdentifier = "my-workspace"
	run.Spec.Definition.Spec.Implementation = []byte(`{"type":"TERRAFORM"}`)
	return run
}

func startTestDispatcher(t *testing.T, ctx context.Context, cfg config.Config, jobs ...*batchv1.Job) (*Dispatcher, *fake.Clientset, dispatcher.InitState) {
	t.Helper()
	clientset := fake.NewClientset()
	for _, job := range jobs {
		_, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	d, err := newDispatcher(ctx, cfg, clientset, log.New(io.Discard, "", 0))
	require.NoError(t, err)
	d.fetchOidcIssuer = func(context.Context) (string, error) { return "https://issuer.example.com", nil }
	state, err := d.Init(ctx)
	require.NoError(t, err)
	return d, clientset, state
}

func Test_Start_CreatesAJobWithItsRunInASecret(t *testing.T) {
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
	d, clientset, _ := startTestDispatcher(t, t.Context(), testConfig(config.JobSpecTemplates{config.RunnerTypeTf: template}))
	run := newTfRun()
	getSecret := func(run dispatcher.Run) *corev1.Secret {
		secret, err := clientset.CoreV1().Secrets(namespace).Get(t.Context(), "run-json-"+run.Metadata.Uuid.String(), metav1.GetOptions{})
		require.NoError(t, err)
		return secret
	}

	t.Run("merges the job skeleton over the template", func(t *testing.T) {
		require.NoError(t, d.Start(t.Context(), run))

		job, err := clientset.BatchV1().Jobs(namespace).Get(t.Context(), "runner-"+run.Metadata.Uuid.String(), metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, runnerUuid.String(), job.Labels[labelRunnerId])
		assert.Equal(t, "platform", job.Labels["team"])
		assert.Equal(t, "TERRAFORM", job.Labels[labelRunnerType])
		assert.Equal(t, int64(45*60), *job.Spec.ActiveDeadlineSeconds)
		assert.Equal(t, int32(0), *job.Spec.BackoffLimit)
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
		assert.Equal(t, resource.MustParse("2Gi"), container.Resources.Limits[corev1.ResourceMemory])
		assert.Equal(t, resource.MustParse("256Mi"), container.Resources.Requests[corev1.ResourceMemory])
	})

	t.Run("stores the run in the Secret of the Job", func(t *testing.T) {
		secret := getSecret(run)
		assert.Equal(t, "Job", secret.OwnerReferences[0].Kind)
		assert.Equal(t, []string{runFile}, slices.Collect(maps.Keys(secret.Data)))
		assert.Contains(t, string(secret.Data[runFile]), `"runToken":"a-run-token"`)

		read, err := d.ReadRun(t.Context(), run.Metadata.Uuid)
		require.NoError(t, err)
		assert.Equal(t, run.Metadata.Uuid, read.Metadata.Uuid)
		assert.Equal(t, "a-run-token", read.Spec.RunToken)
	})

	t.Run("fails for a run too large for a Secret", func(t *testing.T) {
		large := newTfRun()
		large.Spec.BuildingBlock.Spec.Inputs = []client.MeshRunnerRunInput{{Key: "large", Value: []byte(`"` + strings.Repeat("x", 1<<20) + `"`)}}

		assert.ErrorIs(t, d.Start(t.Context(), large), dispatcher.ErrRunTooLarge)
	})

	t.Run("retries a transient create error and takes AlreadyExists for success", func(t *testing.T) {
		var secretCreates, jobCreates int
		clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			secretCreates++
			return secretCreates == 1, nil, apierrors.NewTooManyRequests("slow down", 0)
		})
		clientset.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
			jobCreates++
			if jobCreates > 1 {
				return false, nil, nil
			}
			create := action.(k8stesting.CreateAction)
			require.NoError(t, clientset.Tracker().Create(create.GetResource(), create.GetObject(), create.GetNamespace()))
			return true, nil, apierrors.NewServiceUnavailable("timed out after the create")
		})
		retried := newTfRun()

		require.NoError(t, d.Start(t.Context(), retried))

		assert.Equal(t, 2, secretCreates)
		assert.Equal(t, 2, jobCreates)
		assert.Equal(t, "runner-"+retried.Metadata.Uuid.String(), getSecret(retried).OwnerReferences[0].Name)
	})

	t.Run("fails at once on a permanent create error", func(t *testing.T) {
		var jobCreates int
		clientset.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
			jobCreates++
			return true, nil, apierrors.NewForbidden(batchv1.Resource("jobs"), "runner", errors.New("quota exceeded"))
		})

		assert.ErrorContains(t, d.Start(t.Context(), newTfRun()), "quota exceeded")
		assert.Equal(t, 1, jobCreates)
	})
}

func Test_Start_RefusesARunTypeOutsideForTypes(t *testing.T) {
	cfg := testConfig(config.JobSpecTemplates{config.RunnerTypeAll: runnerTemplate(corev1.Container{Image: "runner"}, corev1.PodSpec{})})
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeManual}
	d, clientset, _ := startTestDispatcher(t, t.Context(), cfg)

	err := d.Start(t.Context(), newTfRun())

	assert.EqualError(t, err, "this runner does not run tf runs")
	jobs, err := clientset.BatchV1().Jobs(namespace).List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, jobs.Items)
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

func receive(t *testing.T, d *Dispatcher) dispatcher.RunFinished {
	t.Helper()
	select {
	case finished := <-d.Finished():
		return finished
	case <-time.After(5 * time.Second):
		t.Fatal("no run finished")
		return dispatcher.RunFinished{}
	}
}

func assertNoneFinished(t *testing.T, d *Dispatcher) {
	t.Helper()
	select {
	case finished := <-d.Finished():
		t.Fatalf("unexpected finished run %+v", finished)
	case <-time.After(100 * time.Millisecond):
	}
}

func Test_InitAndFinished_ReportTheJobsOfThisRunner(t *testing.T) {
	running, ending, failed, reported, succeeded := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	reportedJob := withCondition(runningJob(reported), batchv1.JobFailed, "BackoffLimitExceeded")
	reportedJob.Annotations = map[string]string{annotationFailureReported: "true"}
	ctx, cancel := context.WithCancel(t.Context())
	d, clientset, state := startTestDispatcher(t, ctx, testConfig(nil),
		runningJob(running),
		runningJob(ending),
		withCondition(runningJob(failed), batchv1.JobFailed, "DeadlineExceeded"),
		reportedJob,
		withCondition(runningJob(succeeded), batchv1.JobComplete, ""),
	)
	jobs := clientset.BatchV1().Jobs(namespace)

	t.Run("Init returns the running Jobs and the workload identity", func(t *testing.T) {
		assert.ElementsMatch(t, []uuid.UUID{running, ending}, state.Running)
		assert.Equal(t, "https://issuer.example.com", *state.WorkloadIdentity.Issuer)
	})

	t.Run("reports the failed Jobs of a previous process", func(t *testing.T) {
		failure := receive(t, d)
		assert.Equal(t, failed, failure.RunUuid)
		assert.EqualError(t, failure.Err, "the Kubernetes Job runner-"+failed.String()+" failed: DeadlineExceeded: details")
		assertNoneFinished(t, d)
	})

	t.Run("annotates a failed Job only once the report is confirmed", func(t *testing.T) {
		annotations := func() map[string]string {
			job, err := jobs.Get(t.Context(), "runner-"+failed.String(), metav1.GetOptions{})
			require.NoError(t, err)
			return job.Annotations
		}
		assert.Empty(t, annotations())

		d.FailureReported(t.Context(), failed)

		assert.Equal(t, map[string]string{annotationFailureReported: "true"}, annotations())
	})

	t.Run("reports a Job that finishes", func(t *testing.T) {
		_, err := jobs.UpdateStatus(t.Context(), withCondition(runningJob(running), batchv1.JobComplete, ""), metav1.UpdateOptions{})
		require.NoError(t, err)
		assert.Equal(t, dispatcher.RunFinished{RunUuid: running}, receive(t, d))

		_, err = jobs.UpdateStatus(t.Context(), withCondition(runningJob(ending), batchv1.JobFailed, "BackoffLimitExceeded"), metav1.UpdateOptions{})
		require.NoError(t, err)
		failure := receive(t, d)
		assert.Equal(t, ending, failure.RunUuid)
		assert.ErrorContains(t, failure.Err, "BackoffLimitExceeded")
		assertNoneFinished(t, d)
	})

	t.Run("closes when the context ends", func(t *testing.T) {
		cancel()

		_, open := <-d.Finished()
		assert.False(t, open)
	})
}

func Test_JobDeletedWhileRunning_FinishesWithAnError(t *testing.T) {
	running := uuid.New()
	d, clientset, state := startTestDispatcher(t, t.Context(), testConfig(nil), runningJob(running))
	assert.Equal(t, []uuid.UUID{running}, state.Running)

	require.NoError(t, clientset.BatchV1().Jobs(namespace).Delete(t.Context(), "runner-"+running.String(), metav1.DeleteOptions{}))

	finished := receive(t, d)
	assert.Equal(t, running, finished.RunUuid)
	assert.EqualError(t, finished.Err, "the Kubernetes Job runner-"+running.String()+" was deleted before it finished")
	assertNoneFinished(t, d)
}

func Test_FailureReportedForAJobThatIsGone_LogsNothing(t *testing.T) {
	var logs strings.Builder
	d, err := newDispatcher(t.Context(), testConfig(nil), fake.NewClientset(), log.New(&logs, "", 0))
	require.NoError(t, err)

	d.FailureReported(t.Context(), uuid.New())

	assert.Empty(t, logs.String())
}

func Test_WorkloadIdentity_MatchesTheServiceAccountAndTokenMounts(t *testing.T) {
	wif := newWorkloadIdentity(namespace, "https://issuer.example.com")

	assert.Equal(t, "system:serviceaccount:runners:workspace.{{ workspaceIdentifier }}.buildingblockdefinition.{{ buildingBlockDefinitionUuid }}", *wif.SubjectTemplate)
	assert.Equal(t, &client.MeshRunnerWifProviderConfig{Audience: "gcp-workload-identity-provider:runners", TokenPath: "/var/run/secrets/workload-identity/gcp/token"}, wif.Gcp)
}
