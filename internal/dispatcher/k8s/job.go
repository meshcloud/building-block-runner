//go:build k8s

package k8s

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"

	"github.com/meshcloud/meshstack-cli/pkg/setting"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	labelName       = "app.kubernetes.io/name"
	labelRunId      = "meshcloud.io/run-id"
	labelRunnerId   = "meshcloud.io/runner-id"
	labelRunnerType = "meshcloud.io/runner-type"

	runnerContainerName = "runner"
	runVolumeName       = "run"
	runDir              = "/var/run/secrets/meshstack"
	workVolumeName      = "work"
	workDir             = "/work"

	runFile     = "run.json"
	runTokenKey = "run-token"
	wifTokenKey = "wif-token"

	// maxSecretBytes leaves room for the metadata of a Secret within the 1 MiB that Kubernetes allows.
	maxSecretBytes = 1<<20 - 10<<10

	// defaultTtlSeconds keeps a finished Job long enough to inspect it, but not so long that it fills the Job quota.
	defaultTtlSeconds = 120
)

type runObjects struct {
	serviceAccount *corev1.ServiceAccount
	secret         *corev1.Secret
	job            *batchv1.Job
}

func (d *Dispatcher) newRunObjects(run dispatcher.Run, runnerType config.RunnerType, wifToken string) (runObjects, error) {
	template, err := d.cfg.K8s.JobSpecTemplates.GetFor(runnerType)
	if err != nil {
		return runObjects{}, err
	}
	secret, err := d.newSecret(run, wifToken)
	if err != nil {
		return runObjects{}, err
	}
	serviceAccount := d.newServiceAccount(run)
	job, err := d.newJob(run, runnerType, template, secret, serviceAccount.Name)
	if err != nil {
		return runObjects{}, err
	}
	return runObjects{serviceAccount, secret, job}, nil
}

func (d *Dispatcher) labels(run dispatcher.Run) map[string]string {
	return map[string]string{
		labelName:     "runner",
		labelRunId:    run.Metadata.Uuid.String(),
		labelRunnerId: d.cfg.Uuid.String(),
	}
}

func (d *Dispatcher) objectMeta(name string, run dispatcher.Run) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: d.cfg.K8s.Namespace, Labels: d.labels(run)}
}

func (d *Dispatcher) newServiceAccount(run dispatcher.Run) *corev1.ServiceAccount {
	name := serviceAccountName(run.Spec.Definition.Spec.WorkspaceIdentifier, run.Spec.Definition.Uuid)
	return &corev1.ServiceAccount{ObjectMeta: d.objectMeta(name, run)}
}

func (d *Dispatcher) newSecret(run dispatcher.Run, wifToken string) (*corev1.Secret, error) {
	runJson, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	if size := len(runJson) + len(wifToken); size > maxSecretBytes {
		return nil, fmt.Errorf("%w: it has %d bytes, a Kubernetes Secret holds %d", dispatcher.ErrRunTooLarge, size, maxSecretBytes)
	}
	data := map[string][]byte{runFile: runJson, runTokenKey: []byte(run.Spec.RunToken)}
	if wifToken != "" {
		data[wifTokenKey] = []byte(wifToken)
	}
	return &corev1.Secret{ObjectMeta: d.objectMeta(secretName(run.Metadata.Uuid.String()), run), Data: data}, nil
}

func hasWifToken(secret *corev1.Secret) bool {
	_, ok := secret.Data[wifTokenKey]
	return ok
}

func secretName(runUuid string) string {
	return "run-json-" + runUuid
}

func jobName(runUuid string) string {
	return "runner-" + runUuid
}

// newJob merges the settings the dispatcher relies on over the template, so that a template cannot override them.
func (d *Dispatcher) newJob(run dispatcher.Run, runnerType config.RunnerType, template config.JobSpecTemplate, secret *corev1.Secret, serviceAccountName string) (*batchv1.Job, error) {
	template = *template.DeepCopy()
	job := &batchv1.Job{ObjectMeta: template.ObjectMeta, Spec: template.Spec}
	labels := d.labels(run)
	labels[labelRunnerType] = runnerType.ForApi()

	job.Name, job.Namespace = jobName(run.Metadata.Uuid.String()), d.cfg.K8s.Namespace
	job.Labels = merge(job.Labels, labels)
	job.Spec.ActiveDeadlineSeconds = new(int64(d.cfg.Dispatcher.Timeout.Seconds()))
	// A second pod would run the run again.
	job.Spec.BackoffLimit = new(int32(0))
	job.Spec.PodFailurePolicy = failJobOnReportedFailure()
	job.Spec.TTLSecondsAfterFinished = cmp.Or(job.Spec.TTLSecondsAfterFinished, new(int32(defaultTtlSeconds)))

	pod := &job.Spec.Template
	pod.Labels = merge(pod.Labels, labels)
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever
	pod.Spec.TerminationGracePeriodSeconds = new(int64(d.cfg.Dispatcher.KillGrace.Seconds()))
	pod.Spec.ServiceAccountName = serviceAccountName
	// The runner mounts only the projected tokens of the per-run service account.
	pod.Spec.AutomountServiceAccountToken = new(false)
	pod.Spec.EnableServiceLinks = new(false)
	pod.Spec.Volumes = replaceByName(pod.Spec.Volumes, volumeName, d.volumes(secret)...)
	env, err := d.env(run, runnerType, secret)
	if err != nil {
		return nil, err
	}

	runner := slices.IndexFunc(pod.Spec.Containers, func(container corev1.Container) bool {
		return container.Name == runnerContainerName && container.Image != ""
	})
	if runner < 0 {
		return nil, fmt.Errorf("the job spec template for %s has no container %s with an image", runnerType, runnerContainerName)
	}
	container := &pod.Spec.Containers[runner]
	container.SecurityContext = restrictedSecurityContext()
	container.Env = replaceByName(container.Env, envVarName, env...)
	container.VolumeMounts = replaceByName(container.VolumeMounts, volumeMountName, d.volumeMounts(hasWifToken(secret))...)
	container.WorkingDir = workDir
	container.Resources.Requests = withDefaults(container.Resources.Requests, corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	})
	// No default CPU limit, as it would throttle the run.
	container.Resources.Limits = withDefaults(container.Resources.Limits, corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("1Gi"),
	})
	return job, nil
}

func (d *Dispatcher) volumes(secret *corev1.Secret) []corev1.Volume {
	const tokenExpirationSeconds = 7200
	volumes := []corev1.Volume{
		{Name: runVolumeName, Secret: &corev1.SecretVolumeSource{SecretName: secret.Name}},
		{Name: workVolumeName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
	if hasWifToken(secret) {
		volumes = append(volumes, corev1.Volume{Name: keycloakWifVolumeName, Secret: &corev1.SecretVolumeSource{
			SecretName: secret.Name,
			Items:      []corev1.KeyToPath{{Key: wifTokenKey, Path: "token"}},
		}})
	}
	for _, token := range d.tokens.all() {
		volumes = append(volumes, corev1.Volume{
			Name: token.volumeName(),
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
					Audience:          token.audience,
					ExpirationSeconds: new(int64(tokenExpirationSeconds)),
					Path:              "token",
				}}},
			},
		})
	}
	return volumes
}

func (d *Dispatcher) volumeMounts(withWifToken bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{Name: runVolumeName, MountPath: runDir, ReadOnly: true},
		{Name: workVolumeName, MountPath: workDir},
	}
	if withWifToken {
		mounts = append(mounts, corev1.VolumeMount{Name: keycloakWifVolumeName, MountPath: keycloakWifTokenDir, ReadOnly: true})
	}
	for _, token := range d.tokens.all() {
		mounts = append(mounts, corev1.VolumeMount{Name: token.volumeName(), MountPath: token.mountPath(), ReadOnly: true})
	}
	return mounts
}

func (d *Dispatcher) env(run dispatcher.Run, runnerType config.RunnerType, secret *corev1.Secret) ([]corev1.EnvVar, error) {
	executorConfig, err := d.cfg.ExecutorConfig(runnerType)
	if err != nil {
		return nil, fmt.Errorf("encode the %s config: %w", runnerType, err)
	}
	runPath := runDir + "/" + runFile
	env := []corev1.EnvVar{
		{Name: executor.EnvRunYml, Value: runPath},
		{Name: "RUN_JSON_FILE_PATH", Value: runPath},
		{Name: executor.EnvConfig, Value: executorConfig},
		{Name: executor.EnvRunnerUuid, Value: d.cfg.Uuid.String()},
		{Name: "RUNNER_API_URL", Value: d.cfg.Api.Url.String()},
		{Name: setting.Endpoint.EnvKey(), Value: dispatcher.MeshStackEndpoint(d.cfg, run)},
		{Name: setting.ApiToken.EnvKey(), ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{Name: secret.Name, Key: runTokenKey}}},
	}
	if hasWifToken(secret) {
		env = append(env, corev1.EnvVar{Name: dispatcher.EnvKeycloakWifToken, Value: keycloakWifTokenPath})
	}
	return env, nil
}

func failJobOnReportedFailure() *batchv1.PodFailurePolicy {
	return &batchv1.PodFailurePolicy{Rules: []batchv1.PodFailurePolicyRule{{
		Action: batchv1.PodFailurePolicyActionFailJob,
		OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
			ContainerName: new(runnerContainerName),
			Operator:      batchv1.PodFailurePolicyOnExitCodesOpIn,
			Values:        []int32{executor.ExitFailedDelivered},
		},
	}}}
}

func restrictedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		RunAsNonRoot:             new(true),
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func merge(base, overrides map[string]string) map[string]string {
	merged := maps.Clone(base)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, overrides)
	return merged
}

func withDefaults(list, defaults corev1.ResourceList) corev1.ResourceList {
	merged := maps.Clone(defaults)
	maps.Copy(merged, list)
	return merged
}

func replaceByName[T any](items []T, name func(T) string, replacements ...T) []T {
	kept := slices.DeleteFunc(items, func(item T) bool {
		return slices.ContainsFunc(replacements, func(replacement T) bool { return name(replacement) == name(item) })
	})
	return append(kept, replacements...)
}

func volumeName(volume corev1.Volume) string          { return volume.Name }
func volumeMountName(mount corev1.VolumeMount) string { return mount.Name }
func envVarName(envVar corev1.EnvVar) string          { return envVar.Name }
