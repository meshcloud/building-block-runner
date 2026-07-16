//go:build k8s || !lean

// This file is the real, client-go-backed half of the k8s dispatcher factory seam cmd/bbrunner
// builds on (see cmd/bbrunner/k8s_dispatcher.go): `|| !lean` keeps it in every default build
// (no tags), while a `-tags lean` build that also omits k8s excludes it (and cluster.go)
// entirely -- the "Kubernetes-free all-types in-process image" variant ("leaner
// run-controller image via build tags"). config.go/registration.go (no client-go import) stay
// unconditional so controllerConfig parsing and registration DTO construction keep working in
// every build.

package k8sjob

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// JobMetrics is the small consumer-side meter interface this package drives its metrics
// through -- structurally satisfied by *dispatch.MetricsCollector's exported methods, so
// k8sjob itself never imports prometheus (depguard).
type JobMetrics interface {
	ObserveJobCreationDuration(runnerUuid string, seconds float64)
	IncJobCreationError(runnerUuid, errorType string)
	IncJobsCreated(runnerUuid string)
	IncServiceAccountCreationError(runnerUuid, errorType string)
	IncServiceAccountsCreated(runnerUuid string)
	IncDecryptionError(runnerUuid string)
}

// KubernetesJobDispatcher is the dissolution target of the former
// internal/controller.KubernetesClient / JobManager: it implements
// dispatch.Dispatcher by creating one Kubernetes Job per claimed run (moved, not changed --
// the manifest shapes, env contract and size guard are byte-identical).
type KubernetesJobDispatcher struct {
	clientset  kubernetes.Interface
	namespace  string
	runnerUuid string
	// apiUrl is stamped into the dispatched Job's RUNNER_API_URL env var (a fallback for
	// building callback URLs; runners prefer the run's own _links.meshstackBaseUrl). It is
	// not part of Config because the api/crypto/registration fields stay in the runner type
	// yaml struct -- only this one k8s-facing use crosses over.
	apiUrl  string
	cfg     config.K8sJobConfig
	crypto  *meshcrypto.MeshCertBasedCrypto
	metrics JobMetrics
	logger  *slog.Logger
}

// dispatchError is a plain string error (not fmt.Errorf/errors.New) so its frozen,
// capitalized, user-facing wire text -- reported verbatim as the run's FAILED status
// message -- does not trip staticcheck's ST1005 (errors, unlike operator status text, are
// conventionally lowercase; these strings are the latter, not the former).
type dispatchError string

func (e dispatchError) Error() string { return string(e) }

// NewKubernetesJobDispatcher lives in cluster.go (real cluster connection, coverage-excluded);
// this file only builds/tests the hermetic, fake-clientset-injectable half.

// NewKubernetesJobDispatcherWithClient builds a dispatcher over a caller-supplied
// clientset -- the seam tests use to inject k8s.io/client-go/kubernetes/fake.
func NewKubernetesJobDispatcherWithClient(clientset kubernetes.Interface, cfg config.K8sJobConfig, runnerUuid, apiUrl string, crypto *meshcrypto.MeshCertBasedCrypto, metrics JobMetrics, logger *slog.Logger) *KubernetesJobDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &KubernetesJobDispatcher{
		clientset:  clientset,
		namespace:  cfg.Namespace,
		runnerUuid: runnerUuid,
		apiUrl:     apiUrl,
		cfg:        cfg,
		crypto:     crypto,
		metrics:    metrics,
		logger:     logger,
	}
}

// Dispatch decrypts the claimed run and creates its Kubernetes Job. Order preserved from
// the former controller.processNextRun: decrypt -> template lookup ->
// size guard -> ServiceAccount/Secret/Job. A decrypt failure is now actively reported FAILED
// with actionable key-mismatch guidance (the former silent "wait for the coordinator
// timeout" quirk is gone) while still incrementing the decryption-error metric; a missing
// template returns *dispatch.UnhandledTypeError with the frozen message; any other
// job creation error's Error() text is the exact FAILED-status message reported to meshfed.
func (k *KubernetesJobDispatcher) Dispatch(run dispatch.ClaimedRun) error {
	decryptedRunJsonBase64, err := meshapi.DecryptRunDetails(run.RawJson, meshapi.NewCertDecryptorFromCrypto(k.crypto))
	if err != nil {
		k.metrics.IncDecryptionError(k.runnerUuid)
		// report an actionable FAILED status (wording aligned with the tf runner's
		// decrypt-failure guidance, internal/tf/run.go) instead of leaving the run to time
		// out silently. Uses the already-accepted reportRunFailure wire shape.
		return dispatchError(fmt.Sprintf("Failed to decrypt run details for run %s: %s. "+
			"This typically indicates a key mismatch - the private key configured for this "+
			"runner does not match the public key used to encrypt the run in meshStack. Please "+
			"verify that the runner's configured private key matches the public key registered "+
			"for this runner in meshStack.", run.Id, err.Error()))
	}

	runnerType := string(run.Type)
	jobSpec, ok := k.cfg.Implementations[runnerType]
	if !ok {
		return &dispatch.UnhandledTypeError{
			Type:    run.Type,
			Message: fmt.Sprintf("no implementation handler configured for type '%s'", runnerType),
		}
	}

	runInfo := run.Run.GetRunInfo()
	if err := k.createRunnerJob(runInfo, decryptedRunJsonBase64, runnerType, &jobSpec); err != nil {
		var tooLarge *RunTooLargeError
		if errors.As(err, &tooLarge) {
			return dispatchError("Run data is too large to be passed to the runner. The run data exceeds the Kubernetes secret size limit of 1MiB. Please reduce the size of the building block inputs.")
		}
		return dispatchError(fmt.Sprintf("Failed to create job for run: %s", err.Error()))
	}

	return nil
}

// InFlight returns the number of runner jobs created by this controller that have not yet
// finished (dispatch.Dispatcher's capacity signal). Finished jobs (Complete or Failed) are
// excluded so that jobs still lingering during their TTLSecondsAfterFinished window do not
// count against the concurrency budget.
func (k *KubernetesJobDispatcher) InFlight() (int, error) {
	selector := fmt.Sprintf("meshcloud.io/runner-id=%s", k.runnerUuid)
	jobList, err := k.clientset.BatchV1().Jobs(k.namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list jobs for capacity check: %w", err)
	}

	active := 0
	for i := range jobList.Items {
		if !isJobFinished(&jobList.Items[i]) {
			active++
		}
	}
	return active, nil
}

func (k *KubernetesJobDispatcher) createRunnerJob(runInfo meshapi.RunInfo, runJsonBase64 string, implType string, jobSpec *config.JobSpecTemplate) error {
	k.logger.Info("preparing to create runner job", "runId", runInfo.Uuid)

	start := time.Now()
	defer func() {
		k.metrics.ObserveJobCreationDuration(k.runnerUuid, time.Since(start).Seconds())
	}()

	jobName := fmt.Sprintf("runner-%s", runInfo.Uuid)
	namespace := k.namespace

	// Check if job already exists
	_, err := k.clientset.BatchV1().Jobs(namespace).Get(context.TODO(), jobName, metav1.GetOptions{})
	if err == nil {
		k.logger.Info("job already exists, skipping creation", "job", jobName)
		return nil
	}

	k.logger.Info("creating job", "job", jobName, "runId", runInfo.Uuid, "runnerUuid", k.runnerUuid, "namespace", namespace)

	// Check if the run JSON data exceeds the Kubernetes secret size limit (1MiB).
	// Kubernetes secrets are limited to 1MiB of data. If the run data is too large,
	// we cannot create a secret and must report the error back to meshfed.
	runJsonSize := estimateRunJsonSize(runJsonBase64)
	if runJsonSize > EffectiveMaxRunJsonSize {
		k.metrics.IncJobCreationError(k.runnerUuid, dispatch.ErrorTypeRunTooLarge)
		return &RunTooLargeError{
			RunId:    runInfo.Uuid,
			DataSize: runJsonSize,
			MaxSize:  EffectiveMaxRunJsonSize,
		}
	}

	// Create service account for workload identity with format: workspace.<workspace>.buildingblockdefinition.<bbd-uuid>
	// Using periods as separators for clear, unambiguous parsing (workspace names can contain dashes)
	// this will eventually be reflected in the sub claim of the jwt token used for workload identity,
	// as sub: "system:serviceaccount:<namespace>:workspace.<bbd-workspace>.buildingblockdefinition.<bbd-uuid>"
	// IMPORTANT: this should align with subject pattern in registration.go BuildRunnerRegistrationDTO()
	serviceAccountName := fmt.Sprintf("workspace.%s.buildingblockdefinition.%s", runInfo.BuildingBlockDefinitionWorkspace, runInfo.BuildingBlockDefinitionUuid)
	err = k.createServiceAccount(namespace, serviceAccountName, runInfo.Uuid)
	if err != nil {
		k.metrics.IncJobCreationError(k.runnerUuid, dispatch.ErrorTypeJobCreation)
		return fmt.Errorf("failed to create service account: %w", err)
	}

	// Create Secret with run JSON data to avoid environment variable size limits.
	// The run data is stored as a file in a secret and mounted into the runner pod.
	runJsonSecretName := fmt.Sprintf("run-json-%s", runInfo.Uuid)
	err = k.createRunJsonSecret(namespace, runJsonSecretName, runJsonBase64, runInfo.Uuid)
	if err != nil {
		k.metrics.IncJobCreationError(k.runnerUuid, dispatch.ErrorTypeJobCreation)
		return fmt.Errorf("failed to create run json secret: %w", err)
	}

	// Create Job with run data mounted as a file via secret volume
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":   "runner",
				"meshcloud.io/run-id":      runInfo.Uuid,
				"meshcloud.io/runner-id":   k.runnerUuid,
				"meshcloud.io/runner-type": implType,
			},
		},
		Spec: batchv1.JobSpec{
			// No retries: a runner Job is a single, potentially state-mutating terraform run, so k8s
			// must never re-run it on pod failure or deletion (a killed pod would otherwise spawn a
			// replacement that repeats the APPLY/DESTROY). Re-triggering is a deliberate user action.
			// The runner reports terminal ABORTED itself on SIGTERM (single-run ctx cancellation), so
			// a killed Job is recorded, not silently retried. Paired with RestartPolicyNever below.
			BackoffLimit: int32Ptr(0),
			// Clean up after 2 minutes of completion.
			// NOTE: it's important this is not too short and not too long:
			// Too short: jobs might be deleted before we can inspect them in case of failures
			// Too long: completed jobs take up from the job quota and can block new jobs from being created
			TTLSecondsAfterFinished: int32Ptr(120),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":   "runner",
						"meshcloud.io/run-id":      runInfo.Uuid,
						"meshcloud.io/runner-id":   k.runnerUuid,
						"meshcloud.io/runner-type": implType,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: serviceAccountName,
					ImagePullSecrets:   k.buildImagePullSecrets(),
					Tolerations:        buildTolerations(k.cfg.Tolerations),
					NodeSelector:       k.cfg.NodeSelector,
					Volumes:            k.createVolumes(namespace, jobSpec, runJsonSecretName),
					// Disable service-link env injection (MARIADB_*, KUBERNETES_* etc.) to keep
					// the container environment clean and avoid leaking service discovery info.
					EnableServiceLinks: new(false),
					// Runner containers only need their own per-run ServiceAccount token, which is
					// mounted explicitly. Suppress the default token mount on the Pod level.
					AutomountServiceAccountToken: new(false),
					Containers: []corev1.Container{
						{
							Name:            "runner",
							Image:           jobSpec.Image,
							Command:         k.buildCommand(jobSpec),
							Args:            k.buildArgs(jobSpec),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             k.buildEnvVars(jobSpec),
							VolumeMounts:    k.createVolumeMounts(jobSpec),
							Resources:       buildResourceRequirements(jobSpec.Resources),
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: new(false),
								RunAsNonRoot:             new(true),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}

	createdJob, err := k.clientset.BatchV1().Jobs(namespace).Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		k.metrics.IncJobCreationError(k.runnerUuid, dispatch.ErrorTypeJobCreation)
		// Clean up the secret since the job failed to create
		_ = k.clientset.CoreV1().Secrets(namespace).Delete(context.TODO(), runJsonSecretName, metav1.DeleteOptions{})
		return fmt.Errorf("failed to create job: %w", err)
	}

	// Set owner reference on the secret so it gets garbage-collected when the job is deleted
	err = k.setSecretOwnerReference(namespace, runJsonSecretName, createdJob)
	if err != nil {
		k.logger.Warn("failed to set owner reference on secret (secret will need manual cleanup)", "secret", runJsonSecretName, "error", err)
	}

	k.metrics.IncJobsCreated(k.runnerUuid)
	k.logger.Info("successfully created job", "job", jobName)
	return nil
}

// isJobFinished reports whether a job has reached a terminal state (completed or failed).
func isJobFinished(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) &&
			condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func int32Ptr(i int32) *int32 {
	return &i
}

func buildTolerations(configs []config.TolerationConfig) []corev1.Toleration {
	if len(configs) == 0 {
		return nil
	}
	tolerations := make([]corev1.Toleration, len(configs))
	for i, c := range configs {
		tolerations[i] = corev1.Toleration{
			Key:               c.Key,
			Operator:          corev1.TolerationOperator(c.Operator),
			Value:             c.Value,
			Effect:            corev1.TaintEffect(c.Effect),
			TolerationSeconds: c.TolerationSeconds,
		}
	}
	return tolerations
}

// MaxKubernetesSecretSize is the maximum size of data in a Kubernetes secret (1MiB).
const MaxKubernetesSecretSize = 1048576

// SecretMetadataOverhead accounts for Kubernetes Secret serialization overhead
// (metadata, labels, key names, etc.) so that the total Secret object stays within limits.
const SecretMetadataOverhead = 10 * 1024 // 10KiB

// EffectiveMaxRunJsonSize is the maximum run JSON size accounting for Secret overhead.
const EffectiveMaxRunJsonSize = MaxKubernetesSecretSize - SecretMetadataOverhead

// RunTooLargeError is returned when the run JSON data exceeds the Kubernetes secret size limit.
type RunTooLargeError struct {
	RunId    string
	DataSize int
	MaxSize  int
}

func (e *RunTooLargeError) Error() string {
	return fmt.Sprintf(
		"run %s data size (%d bytes) exceeds Kubernetes secret limit (%d bytes)",
		e.RunId, e.DataSize, e.MaxSize,
	)
}

// estimateRunJsonSize returns an estimate of the decoded run JSON data size in bytes.
// Uses DecodedLen to estimate the maximum size without performing a full decode,
// avoiding redundant decoding since the data is decoded again when building the Secret.
func estimateRunJsonSize(runJsonBase64 string) int {
	return base64.StdEncoding.DecodedLen(len(runJsonBase64))
}

// Default resource values for runner containers.
const (
	DefaultCpuRequest    = "100m"
	DefaultCpuLimit      = "" // No default CPU limit to avoid throttling
	DefaultMemoryRequest = "256Mi"
	DefaultMemoryLimit   = "1Gi"
)

// buildResourceRequirements creates Kubernetes resource requirements from config, applying defaults.
// CPU limit is not set by default to avoid throttling issues.
func buildResourceRequirements(resources config.ResourcesConfig) corev1.ResourceRequirements {
	cpuRequest := DefaultCpuRequest
	if resources.Requests.Cpu != "" {
		cpuRequest = resources.Requests.Cpu
	}

	cpuLimit := DefaultCpuLimit
	if resources.Limits.Cpu != "" {
		cpuLimit = resources.Limits.Cpu
	}

	memoryRequest := DefaultMemoryRequest
	if resources.Requests.Memory != "" {
		memoryRequest = resources.Requests.Memory
	}

	memoryLimit := DefaultMemoryLimit
	if resources.Limits.Memory != "" {
		memoryLimit = resources.Limits.Memory
	}

	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuRequest),
			corev1.ResourceMemory: resource.MustParse(memoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(memoryLimit),
		},
	}

	// Only set CPU limit if explicitly configured (not empty)
	if cpuLimit != "" {
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(cpuLimit)
	}

	return requirements
}

func (k *KubernetesJobDispatcher) buildImagePullSecrets() []corev1.LocalObjectReference {
	if len(k.cfg.ImagePullSecrets) == 0 {
		return nil
	}

	imagePullSecrets := make([]corev1.LocalObjectReference, len(k.cfg.ImagePullSecrets))
	for i, secret := range k.cfg.ImagePullSecrets {
		imagePullSecrets[i] = corev1.LocalObjectReference{Name: secret}
	}
	return imagePullSecrets
}

func (k *KubernetesJobDispatcher) buildCommand(jobSpec *config.JobSpecTemplate) []string {
	// Return custom command from job spec if provided
	if len(jobSpec.Command) > 0 {
		return jobSpec.Command
	}

	// No custom command: use default container entrypoint
	return nil
}

func (k *KubernetesJobDispatcher) buildArgs(jobSpec *config.JobSpecTemplate) []string {
	// Return custom args from job spec if provided
	if len(jobSpec.Args) > 0 {
		return jobSpec.Args
	}

	// No custom args: use default
	return nil
}

func (k *KubernetesJobDispatcher) createServiceAccount(namespace, name, runID string) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "runner",
				"meshcloud.io/run-id":    runID,
				"meshcloud.io/runner-id": k.runnerUuid,
			},
		},
	}

	// Check if service account already exists
	_, err := k.clientset.CoreV1().ServiceAccounts(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err == nil {
		k.logger.Info("service account already exists, skipping creation", "serviceAccount", name)
		return nil
	}

	_, err = k.clientset.CoreV1().ServiceAccounts(namespace).Create(context.TODO(), serviceAccount, metav1.CreateOptions{})
	if err != nil {
		k.metrics.IncServiceAccountCreationError(k.runnerUuid, dispatch.ErrorTypeServiceAccountCreation)
		return fmt.Errorf("failed to create service account: %w", err)
	}

	k.metrics.IncServiceAccountsCreated(k.runnerUuid)
	k.logger.Info("successfully created service account", "serviceAccount", name, "namespace", namespace)
	return nil
}

// createRunJsonSecret creates a Kubernetes secret containing the run JSON data.
// The data is stored as a decoded JSON file (not base64-encoded) so runners can read it directly.
func (k *KubernetesJobDispatcher) createRunJsonSecret(namespace, secretName, runJsonBase64, runID string) error {
	// Decode base64 to get the raw JSON bytes for the secret
	runJsonBytes, err := base64.StdEncoding.DecodeString(runJsonBase64)
	if err != nil {
		return fmt.Errorf("failed to decode run json base64: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "runner",
				"meshcloud.io/run-id":    runID,
				"meshcloud.io/runner-id": k.runnerUuid,
			},
		},
		Data: map[string][]byte{
			"run.json": runJsonBytes,
		},
	}

	// Check if secret already exists
	_, err = k.clientset.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err == nil {
		k.logger.Info("secret already exists, skipping creation", "secret", secretName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check if secret %s exists: %w", secretName, err)
	}

	_, err = k.clientset.CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	k.logger.Info("successfully created run json secret", "secret", secretName, "namespace", namespace)
	return nil
}

// setSecretOwnerReference updates a secret to set an owner reference to the given job.
// This ensures the secret is automatically garbage-collected when the job is deleted.
func (k *KubernetesJobDispatcher) setSecretOwnerReference(namespace, secretName string, job *batchv1.Job) error {
	secret, err := k.clientset.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get secret for owner reference update: %w", err)
	}

	blockOwnerDeletion := true
	isController := true
	secret.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion:         "batch/v1",
			Kind:               "Job",
			Name:               job.Name,
			UID:                job.UID,
			BlockOwnerDeletion: &blockOwnerDeletion,
			Controller:         &isController,
		},
	}

	_, err = k.clientset.CoreV1().Secrets(namespace).Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret with owner reference: %w", err)
	}

	k.logger.Info("successfully set owner reference on secret", "secret", secretName, "job", job.Name)
	return nil
}

func (k *KubernetesJobDispatcher) createVolumes(namespace string, jobSpec *config.JobSpecTemplate, runJsonSecretName string) []corev1.Volume {
	expirationSeconds := int64(7200)

	volumes := []corev1.Volume{
		{
			Name: "run-json",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: runJsonSecretName,
				},
			},
		},
		{
			Name: "service-account-token-azure",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience:          "api://AzureADTokenExchange",
								ExpirationSeconds: &expirationSeconds,
								Path:              "token",
							},
						},
					},
				},
			},
		},
		{
			Name: "service-account-token-gcp",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience:          fmt.Sprintf("gcp-workload-identity-provider:%s", namespace),
								ExpirationSeconds: &expirationSeconds,
								Path:              "token",
							},
						},
					},
				},
			},
		},
		{
			Name: "service-account-token-aws",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience:          fmt.Sprintf("aws-workload-identity-provider:%s", namespace),
								ExpirationSeconds: &expirationSeconds,
								Path:              "token",
							},
						},
					},
				},
			},
		},
	}

	// Add extra volumes from job spec
	for _, extraVol := range jobSpec.ExtraVolumes {
		volume := corev1.Volume{Name: extraVol.Name}

		// Set the appropriate volume source based on what's configured
		if extraVol.ConfigMap != nil {
			volume.ConfigMap = &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: extraVol.ConfigMap.Name,
				},
			}
		} else if extraVol.Secret != nil {
			volume.Secret = &corev1.SecretVolumeSource{
				SecretName: extraVol.Secret.SecretName,
			}
		} else if extraVol.EmptyDir != nil {
			emptyDir := &corev1.EmptyDirVolumeSource{}
			if extraVol.EmptyDir.SizeLimit != "" {
				// Parse size limit if provided
				quantity, err := resource.ParseQuantity(extraVol.EmptyDir.SizeLimit)
				if err == nil {
					emptyDir.SizeLimit = &quantity
				}
			}
			volume.EmptyDir = emptyDir
		}

		volumes = append(volumes, volume)
	}

	return volumes
}

func (k *KubernetesJobDispatcher) createVolumeMounts(jobSpec *config.JobSpecTemplate) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "run-json",
			MountPath: "/var/run/secrets/meshstack",
			ReadOnly:  true,
		},
		{
			Name:      "service-account-token-azure",
			MountPath: "/var/run/secrets/workload-identity/azure",
			ReadOnly:  true,
		},
		{
			Name:      "service-account-token-gcp",
			MountPath: "/var/run/secrets/workload-identity/gcp",
			ReadOnly:  true,
		},
		{
			Name:      "service-account-token-aws",
			MountPath: "/var/run/secrets/workload-identity/aws",
			ReadOnly:  true,
		},
	}

	// Add extra volume mounts from job spec
	for _, extraMount := range jobSpec.ExtraVolumeMounts {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      extraMount.Name,
			MountPath: extraMount.MountPath,
			ReadOnly:  extraMount.ReadOnly,
		})
	}

	return volumeMounts
}

// runJsonFilePath is the path where the run JSON file is mounted inside the runner container.
const runJsonFilePath = "/var/run/secrets/meshstack/run.json"

// buildEnvVars constructs the environment variables for the runner container.
func (k *KubernetesJobDispatcher) buildEnvVars(jobSpec *config.JobSpecTemplate) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			// Path to the mounted run JSON file containing the decrypted run specification.
			Name:  "RUN_JSON_FILE_PATH",
			Value: runJsonFilePath,
		},
		{
			Name:  "RUNNER_UUID",
			Value: k.runnerUuid,
		},
		{
			// The API URL is used as a fallback for building callback URLs.
			// Runners prefer the _links.meshstackBaseUrl from the run object when available.
			Name:  "RUNNER_API_URL",
			Value: k.apiUrl,
		},
	}

	// Add custom environment variables from job spec
	for key, value := range jobSpec.Env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}

	return envVars
}
