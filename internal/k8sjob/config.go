package k8sjob

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Config holds the Kubernetes-specific configuration for KubernetesJobDispatcher: the
// dissolution target of the former internal/controller/config.go's k8s-facing fields
// (PLAN_DETAIL_05 §5) -- namespace, job templates, tolerations, node selector and image
// pull secrets. Registration/crypto/api fields stay in the persona (cmd/bbrunner) config,
// which embeds this struct inline (`yaml:",inline"`) so every existing yaml key parses
// byte-identically (D7).
type Config struct {
	Namespace        string                     `yaml:"namespace"`        // Kubernetes namespace where jobs are created
	ImagePullSecrets []string                   `yaml:"imagePullSecrets"` // Image pull secrets for runner jobs (optional)
	Tolerations      []TolerationConfig         `yaml:"tolerations"`      // Pod tolerations applied to all runner jobs (e.g. for spot instances)
	NodeSelector     map[string]string          `yaml:"nodeSelector"`     // Node selector applied to all runner jobs
	Implementations  map[string]JobSpecTemplate `yaml:"implementations"`  // Kubernetes job templates keyed by implementation type (e.g. TERRAFORM, GITHUB_WORKFLOW)
}

// JobSpecTemplate defines the Kubernetes job specification for a runner.
// All configuration is passed via environment variables.
type JobSpecTemplate struct {
	Image             string             `yaml:"image"`             // Container image to use for the runner
	Command           []string           `yaml:"command"`           // Optional: Override container command for custom entrypoint wrapper
	Args              []string           `yaml:"args"`              // Optional: Override container args for custom entrypoint wrapper
	Env               map[string]string  `yaml:"env"`               // Additional environment variables to pass to the runner
	Resources         ResourcesConfig    `yaml:"resources"`         // Container resource requests and limits
	ExtraVolumes      []ExtraVolume      `yaml:"extraVolumes"`      // Additional volumes to mount (e.g., for trusted certs)
	ExtraVolumeMounts []ExtraVolumeMount `yaml:"extraVolumeMounts"` // Additional volume mounts
}

// TolerationConfig defines a pod toleration for scheduling runner jobs on tainted nodes.
// Operator defaults to "Equal" when Value is set, and "Exists" when only Key is set.
// Effect can be "NoSchedule", "PreferNoSchedule", or "NoExecute".
type TolerationConfig struct {
	Key               string `yaml:"key"`
	Operator          string `yaml:"operator"`
	Value             string `yaml:"value"`
	Effect            string `yaml:"effect"`
	TolerationSeconds *int64 `yaml:"tolerationSeconds"` // Only meaningful for "NoExecute" effect
}

// ExtraVolume defines an additional volume with support for ConfigMap, Secret, or EmptyDir sources.
// Only one of ConfigMap, Secret, or EmptyDir should be set.
type ExtraVolume struct {
	Name      string               `yaml:"name"`      // Volume name
	ConfigMap *ConfigMapVolumeSpec `yaml:"configMap"` // ConfigMap volume source (optional)
	Secret    *SecretVolumeSpec    `yaml:"secret"`    // Secret volume source (optional)
	EmptyDir  *EmptyDirVolumeSpec  `yaml:"emptyDir"`  // EmptyDir volume source (optional)
}

// ConfigMapVolumeSpec defines a ConfigMap volume source.
type ConfigMapVolumeSpec struct {
	Name string `yaml:"name"` // ConfigMap name
}

// SecretVolumeSpec defines a Secret volume source.
type SecretVolumeSpec struct {
	SecretName string `yaml:"secretName"` // Secret name
}

// EmptyDirVolumeSpec defines an EmptyDir volume source.
type EmptyDirVolumeSpec struct {
	SizeLimit string `yaml:"sizeLimit"` // Optional size limit (e.g., "1Gi")
}

// ExtraVolumeMount defines an additional volume mount.
type ExtraVolumeMount struct {
	Name      string `yaml:"name"`      // Volume name (must match volume)
	MountPath string `yaml:"mountPath"` // Path to mount in container
	ReadOnly  bool   `yaml:"readOnly"`  // Mount as read-only
}

// ResourcesConfig defines CPU and memory resource requests and limits for the runner container.
type ResourcesConfig struct {
	Requests ResourceSpec `yaml:"requests"` // Resource requests (guaranteed minimum)
	Limits   ResourceSpec `yaml:"limits"`   // Resource limits (maximum allowed)
}

// ResourceSpec defines CPU and memory values.
type ResourceSpec struct {
	Cpu    string `yaml:"cpu"`    // CPU in Kubernetes format (e.g., "100m", "1")
	Memory string `yaml:"memory"` // Memory in Kubernetes format (e.g., "256Mi", "1Gi")
}

// DefaultMaxConcurrentJobs is the number of unfinished runner jobs the controller keeps in
// flight when maxConcurrentJobs is not configured. This caps how many runs the controller
// claims so it does not fetch (and thereby fail) runs it cannot place.
//
// PLAN_DETAIL_05_dispatcher.md §12/§16 sanctioned delta 6: this default changes 20 -> 10 in
// this phase (a more conservative default for cluster job pressure); operators can still
// set any value via maxConcurrentJobs.
const DefaultMaxConcurrentJobs = 10

// Validate checks the Kubernetes-specific configuration fields. Namespace and at least one
// valid implementation handler are required; unknown/ALL implementation keys and missing
// images are rejected with the same messages the former controller/config.go produced.
func (c Config) Validate() error {
	if c.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if len(c.Implementations) == 0 {
		return fmt.Errorf("at least one implementation handler must be configured under 'implementations'")
	}
	for key, spec := range c.Implementations {
		if !isValidHandlerImplementationType(key) {
			return fmt.Errorf("implementations key '%s' is invalid; valid values are: TERRAFORM, GITHUB_WORKFLOW, GITLAB_PIPELINE, AZURE_DEVOPS_PIPELINE, MANUAL", key)
		}
		if spec.Image == "" {
			return fmt.Errorf("implementations.%s.image is required", key)
		}
	}
	return nil
}

// isValidHandlerImplementationType checks if the given implementation type is a valid handler key.
// Note: "ALL" is a registration concept only and cannot be used as a handler key.
func isValidHandlerImplementationType(implType string) bool {
	switch meshapi.RunnerImplementationType(implType) {
	case meshapi.RunnerTypeTerraform,
		meshapi.RunnerTypeGitHubWorkflow,
		meshapi.RunnerTypeGitLabPipeline,
		meshapi.RunnerTypeAzureDevOpsPipeline,
		meshapi.RunnerTypeManual:
		return true
	default:
		return false
	}
}
