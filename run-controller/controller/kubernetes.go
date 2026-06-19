package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubernetesClient struct {
	clientset *kubernetes.Clientset
	namespace string
	logger    *log.Logger
}

func newKubernetesClient(namespace string, logger *log.Logger) (*KubernetesClient, error) {
	config, err := getKubernetesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &KubernetesClient{
		clientset: clientset,
		namespace: namespace,
		logger:    logger,
	}, nil
}

func getKubernetesConfig() (*rest.Config, error) {
	// This uses the standard Kubernetes client-go precedence:
	// 1. In-cluster config (when running inside a pod)
	// 2. KUBECONFIG environment variable
	// 3. $HOME/.kube/config
	// This is the idiomatic way and handles all edge cases properly
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return kubeConfig.ClientConfig()
}

func (k *KubernetesClient) CreateRunnerJob(runInfo meshapi.RunInfo, runJsonBase64 string, implType string, jobSpec *JobSpecTemplate, metrics *MetricsCollector) error {
	k.logger.Printf("Preparing to create runner job for run %s", runInfo.Uuid)

	// Measure job creation duration
	start := time.Now()
	defer func() {
		metrics.jobCreationDuration.WithLabelValues(AppConfig.Uuid).Observe(time.Since(start).Seconds())
	}()

	jobName := fmt.Sprintf("runner-%s", runInfo.Uuid)

	// Use namespace from controller config
	namespace := AppConfig.Namespace

	// Check if job already exists
	_, err := k.clientset.BatchV1().Jobs(namespace).Get(context.TODO(), jobName, metav1.GetOptions{})
	if err == nil {
		k.logger.Printf("Job %s already exists, skipping creation", jobName)
		return nil
	}

	k.logger.Printf("Creating job %s for run %s with controller %s in namespace %s", jobName, runInfo.Uuid, AppConfig.Uuid, namespace)

	// Check if the run JSON data exceeds the Kubernetes secret size limit (1MiB).
	// Kubernetes secrets are limited to 1MiB of data. If the run data is too large,
	// we cannot create a secret and must report the error back to meshfed.
	runJsonSize, err := estimateRunJsonSize(runJsonBase64)
	if err != nil {
		metrics.jobCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeJobCreation).Inc()
		return fmt.Errorf("failed to estimate run json size: %w", err)
	}
	if runJsonSize > EffectiveMaxRunJsonSize {
		metrics.jobCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeRunTooLarge).Inc()
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
	// IMPORTANT: this should align with subject pattern in dtos.go BuildRunnerRegistrationDTO()
	serviceAccountName := fmt.Sprintf("workspace.%s.buildingblockdefinition.%s", runInfo.BuildingBlockDefinitionWorkspace, runInfo.BuildingBlockDefinitionUuid)
	err = k.createServiceAccount(namespace, serviceAccountName, runInfo.Uuid, metrics)
	if err != nil {
		metrics.jobCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeJobCreation).Inc()
		return fmt.Errorf("failed to create service account: %w", err)
	}

	// Create Secret with run JSON data to avoid environment variable size limits.
	// The run data is stored as a file in a secret and mounted into the runner pod.
	runJsonSecretName := fmt.Sprintf("run-json-%s", runInfo.Uuid)
	err = k.createRunJsonSecret(namespace, runJsonSecretName, runJsonBase64, runInfo.Uuid)
	if err != nil {
		metrics.jobCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeJobCreation).Inc()
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
				"meshcloud.io/runner-id":   AppConfig.Uuid,
				"meshcloud.io/runner-type": implType,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: int32Ptr(1),
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
						"meshcloud.io/runner-id":   AppConfig.Uuid,
						"meshcloud.io/runner-type": implType,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: serviceAccountName,
					ImagePullSecrets:   k.buildImagePullSecrets(),
					Tolerations:        buildTolerations(AppConfig.Tolerations),
					NodeSelector:       AppConfig.NodeSelector,
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
		metrics.jobCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeJobCreation).Inc()
		// Clean up the secret since the job failed to create
		_ = k.clientset.CoreV1().Secrets(namespace).Delete(context.TODO(), runJsonSecretName, metav1.DeleteOptions{})
		return fmt.Errorf("failed to create job: %w", err)
	}

	// Set owner reference on the secret so it gets garbage-collected when the job is deleted
	err = k.setSecretOwnerReference(namespace, runJsonSecretName, createdJob)
	if err != nil {
		k.logger.Printf("Warning: failed to set owner reference on secret %s: %v (secret will need manual cleanup)", runJsonSecretName, err)
	}

	metrics.jobsCreatedTotal.WithLabelValues(AppConfig.Uuid).Inc()
	k.logger.Printf("Successfully created job %s", jobName)
	return nil
}

func int32Ptr(i int32) *int32 {
	return &i
}

func buildTolerations(configs []TolerationConfig) []corev1.Toleration {
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

// MaxKubernetesSecretSize is the maximum size of data in a Kubernetes secret (1MiB)
const MaxKubernetesSecretSize = 1048576

// SecretMetadataOverhead accounts for Kubernetes Secret serialization overhead
// (metadata, labels, key names, etc.) so that the total Secret object stays within limits.
const SecretMetadataOverhead = 10 * 1024 // 10KiB

// EffectiveMaxRunJsonSize is the maximum run JSON size accounting for Secret overhead
const EffectiveMaxRunJsonSize = MaxKubernetesSecretSize - SecretMetadataOverhead

// RunTooLargeError is returned when the run JSON data exceeds the Kubernetes secret size limit.
// This error should be caught by the controller to report back to meshfed.
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
func estimateRunJsonSize(runJsonBase64 string) (int, error) {
	estimatedSize := base64.StdEncoding.DecodedLen(len(runJsonBase64))
	return estimatedSize, nil
}

// Default resource values for runner containers
const (
	DefaultCpuRequest    = "100m"
	DefaultCpuLimit      = "" // No default CPU limit to avoid throttling
	DefaultMemoryRequest = "256Mi"
	DefaultMemoryLimit   = "1Gi"
)

// buildResourceRequirements creates Kubernetes resource requirements from config, applying defaults
// CPU limit is not set by default to avoid throttling issues
func buildResourceRequirements(config ResourcesConfig) corev1.ResourceRequirements {
	cpuRequest := DefaultCpuRequest
	if config.Requests.Cpu != "" {
		cpuRequest = config.Requests.Cpu
	}

	cpuLimit := DefaultCpuLimit
	if config.Limits.Cpu != "" {
		cpuLimit = config.Limits.Cpu
	}

	memoryRequest := DefaultMemoryRequest
	if config.Requests.Memory != "" {
		memoryRequest = config.Requests.Memory
	}

	memoryLimit := DefaultMemoryLimit
	if config.Limits.Memory != "" {
		memoryLimit = config.Limits.Memory
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

func (k *KubernetesClient) buildImagePullSecrets() []corev1.LocalObjectReference {
	if len(AppConfig.ImagePullSecrets) == 0 {
		return nil
	}

	imagePullSecrets := make([]corev1.LocalObjectReference, len(AppConfig.ImagePullSecrets))
	for i, secret := range AppConfig.ImagePullSecrets {
		imagePullSecrets[i] = corev1.LocalObjectReference{Name: secret}
	}
	return imagePullSecrets
}

func (k *KubernetesClient) buildCommand(jobSpec *JobSpecTemplate) []string {
	// Return custom command from job spec if provided
	if len(jobSpec.Command) > 0 {
		return jobSpec.Command
	}

	// No custom command: use default container entrypoint
	return nil
}

func (k *KubernetesClient) buildArgs(jobSpec *JobSpecTemplate) []string {
	// Return custom args from job spec if provided
	if len(jobSpec.Args) > 0 {
		return jobSpec.Args
	}

	// No custom args: use default
	return nil
}

func (k *KubernetesClient) createServiceAccount(namespace, name, runID string, metrics *MetricsCollector) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "runner",
				"meshcloud.io/run-id":    runID,
				"meshcloud.io/runner-id": AppConfig.Uuid,
			},
		},
	}

	// Check if service account already exists
	_, err := k.clientset.CoreV1().ServiceAccounts(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err == nil {
		k.logger.Printf("Service account %s already exists, skipping creation", name)
		return nil
	}

	_, err = k.clientset.CoreV1().ServiceAccounts(namespace).Create(context.TODO(), serviceAccount, metav1.CreateOptions{})
	if err != nil {
		metrics.serviceAccountCreationErrors.WithLabelValues(AppConfig.Uuid, ErrorTypeServiceAccountCreation).Inc()
		return fmt.Errorf("failed to create service account: %w", err)
	}

	metrics.serviceAccountsCreatedTotal.WithLabelValues(AppConfig.Uuid).Inc()
	k.logger.Printf("Successfully created service account %s in namespace %s", name, namespace)
	return nil
}

// createRunJsonSecret creates a Kubernetes secret containing the run JSON data.
// The data is stored as a decoded JSON file (not base64-encoded) so runners can read it directly.
func (k *KubernetesClient) createRunJsonSecret(namespace, secretName, runJsonBase64, runID string) error {
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
				"meshcloud.io/runner-id": AppConfig.Uuid,
			},
		},
		Data: map[string][]byte{
			"run.json": runJsonBytes,
		},
	}

	// Check if secret already exists
	_, err = k.clientset.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err == nil {
		k.logger.Printf("Secret %s already exists, skipping creation", secretName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check if secret %s exists: %w", secretName, err)
	}

	_, err = k.clientset.CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	k.logger.Printf("Successfully created run json secret %s in namespace %s", secretName, namespace)
	return nil
}

// setSecretOwnerReference updates a secret to set an owner reference to the given job.
// This ensures the secret is automatically garbage-collected when the job is deleted.
func (k *KubernetesClient) setSecretOwnerReference(namespace, secretName string, job *batchv1.Job) error {
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

	k.logger.Printf("Successfully set owner reference on secret %s to job %s", secretName, job.Name)
	return nil
}

func (k *KubernetesClient) createVolumes(namespace string, jobSpec *JobSpecTemplate, runJsonSecretName string) []corev1.Volume {
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
			volume.VolumeSource.ConfigMap = &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: extraVol.ConfigMap.Name,
				},
			}
		} else if extraVol.Secret != nil {
			volume.VolumeSource.Secret = &corev1.SecretVolumeSource{
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
			volume.VolumeSource.EmptyDir = emptyDir
		}

		volumes = append(volumes, volume)
	}

	return volumes
}

func (k *KubernetesClient) createVolumeMounts(jobSpec *JobSpecTemplate) []corev1.VolumeMount {
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

// runJsonFilePath is the path where the run JSON file is mounted inside the runner container
const runJsonFilePath = "/var/run/secrets/meshstack/run.json"

// buildEnvVars constructs the environment variables for the runner container
func (k *KubernetesClient) buildEnvVars(jobSpec *JobSpecTemplate) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			// Path to the mounted run JSON file containing the decrypted run specification.
			Name:  "RUN_JSON_FILE_PATH",
			Value: runJsonFilePath,
		},
		{
			Name:  "RUNNER_UUID",
			Value: AppConfig.Uuid,
		},
		{
			// The API URL is used as a fallback for building callback URLs.
			// Runners prefer the _links.meshstackBaseUrl from the run object when available.
			Name:  "RUNNER_API_URL",
			Value: AppConfig.Api.Url,
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

// DiscoverOIDCIssuer attempts to discover the OIDC issuer URL from the Kubernetes API server.
// It queries the /.well-known/openid-configuration endpoint on the API server.
// Returns empty string if discovery fails (e.g., not running in cluster or OIDC not configured).
func DiscoverOIDCIssuer(logger *log.Logger) string {
	config, err := getKubernetesConfig()
	if err != nil {
		logger.Printf("Failed to get Kubernetes config for OIDC discovery: %v", err)
		return ""
	}

	// Build the OIDC configuration URL from the API server host
	// The Kubernetes API server exposes /.well-known/openid-configuration
	apiServerURL := strings.TrimSuffix(config.Host, "/")
	oidcConfigURL := apiServerURL + "/.well-known/openid-configuration"

	logger.Printf("Discovering OIDC issuer from: %s", oidcConfigURL)

	// Create HTTP client with the same TLS config as the Kubernetes client
	transport, err := rest.TransportFor(config)
	if err != nil {
		logger.Printf("Failed to create transport for OIDC discovery: %v", err)
		return ""
	}

	client := &http.Client{Transport: transport}

	resp, err := client.Get(oidcConfigURL)
	if err != nil {
		logger.Printf("Failed to fetch OIDC configuration: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Printf("OIDC configuration endpoint returned status %d", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Printf("Failed to read OIDC configuration response: %v", err)
		return ""
	}

	// Parse the OpenID configuration to extract the issuer
	var oidcConfig struct {
		Issuer string `json:"issuer"`
	}

	if err := json.Unmarshal(body, &oidcConfig); err != nil {
		logger.Printf("Failed to parse OIDC configuration: %v", err)
		return ""
	}

	if oidcConfig.Issuer == "" {
		logger.Printf("OIDC configuration does not contain an issuer")
		return ""
	}

	logger.Printf("Discovered OIDC issuer: %s", oidcConfig.Issuer)
	return oidcConfig.Issuer
}
