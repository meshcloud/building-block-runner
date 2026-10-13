//go:build k8s

package config

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

func TestLoadK8s(t *testing.T) {
	t.Run("runner-config-new.yml", func(t *testing.T) {
		unsetRunnerEnv(t)
		api := newApi("http://host.minikube.internal:8080", "your-client-id", "your-client-secret")
		api.Auth.Profile = "local-dev"
		api.Auth.Workspace = "admin-customer"
		assert.Equal(t, Config{
			Api:    api,
			Crypto: crypto.Crypto{PrivateKeyFiles: crypto.PrivateKeyFiles{{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}}},
			Dispatcher: Dispatcher{
				ForTypes:          RunnerTypes{RunnerTypeAll},
				Register:          &Register{DisplayName: "meshcloud-hosted Runner"},
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(45),
			},
			K8s: &K8s{
				Namespace: "default",
				JobSpecTemplates: JobSpecTemplates{
					RunnerTypeAll: {Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Tolerations:  []corev1.Toleration{spotToleration},
							NodeSelector: map[string]string{"spot": "true"},
							Containers:   []corev1.Container{{Name: "runner", Env: []corev1.EnvVar{{Name: "LOG_LEVEL", Value: "debug"}}}},
						}},
					}},
					RunnerTypeTf:          runnerContainerTemplate("ghcr.io/meshcloud/tf-block-runner:main", executionModeSingleRun),
					RunnerTypeManual:      runnerContainerTemplate("ghcr.io/meshcloud/manual-block-runner:main", springProfileKubernetes),
					RunnerTypeGitHub:      runnerContainerTemplate("ghcr.io/meshcloud/github-block-runner:main", springProfileKubernetes),
					RunnerTypeGitLab:      runnerContainerTemplate("ghcr.io/meshcloud/gitlab-block-runner:main", springProfileKubernetes),
					RunnerTypeAzureDevOps: runnerContainerTemplate("ghcr.io/meshcloud/azure-devops-block-runner:main", springProfileKubernetes),
				},
			},
			Tf: Tf{
				TfParentWorkingDir: "/tmp/runner/wd",
				TfInstallDir:       "/tmp/runner/tfbin",
				WsTimeout:          xtime.Minutes(5),
				InitTimeout:        xtime.Minutes(3),
			},
		}, load(t, "runner-config-new.yml"))
	})

	t.Run("runner-config-old-k8s.yml", func(t *testing.T) {
		unsetRunnerEnv(t)
		assert.Equal(t, Config{
			Uuid:   uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f"),
			Api:    newApiWithWorkspace("http://host.minikube.internal:8080", "admin-customer"),
			Crypto: crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}},
			Dispatcher: Dispatcher{
				ForTypes:          RunnerTypes{RunnerTypeAll},
				Register:          &Register{DisplayName: "meshcloud-hosted Runner"},
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(60),
			},
			K8s: &K8s{
				Namespace: "default",
				JobSpecTemplates: JobSpecTemplates{
					RunnerTypeManual:      runnerContainerTemplate("ghcr.io/meshcloud/manual-block-runner:main", springProfileKubernetes),
					RunnerTypeGitHub:      runnerContainerTemplate("ghcr.io/meshcloud/github-block-runner:main", springProfileKubernetes),
					RunnerTypeGitLab:      runnerContainerTemplate("ghcr.io/meshcloud/gitlab-block-runner:main", springProfileKubernetes),
					RunnerTypeAzureDevOps: runnerContainerTemplate("ghcr.io/meshcloud/azure-devops-block-runner:main", springProfileKubernetes),
					RunnerTypeTf:          runnerContainerTemplate("ghcr.io/meshcloud/tf-block-runner:main", executionModeSingleRun),
				},
			},
			Tf: Tf{
				TfParentWorkingDir: "/tmp/runner/wd",
				TfInstallDir:       "/tmp/runner/tfbin",
				WsTimeout:          xtime.Minutes(5),
				InitTimeout:        xtime.Minutes(3),
			},
		}, load(t, "runner-config-old-k8s.yml"))
	})
}

func TestJobSpecTemplatesGetFor(t *testing.T) {
	logLevel := corev1.EnvVar{Name: "LOG_LEVEL", Value: "debug"}
	all := podSpecTemplate(corev1.PodSpec{
		Tolerations: []corev1.Toleration{spotToleration},
		Containers:  []corev1.Container{{Name: "runner", Env: []corev1.EnvVar{logLevel}}},
	})
	manual := runnerContainerTemplate("ghcr.io/meshcloud/manual-block-runner:main", springProfileKubernetes)

	t.Run("merges the type into all", func(t *testing.T) {
		merged, err := JobSpecTemplates{RunnerTypeAll: all, RunnerTypeManual: manual}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, podSpecTemplate(corev1.PodSpec{
			Tolerations: []corev1.Toleration{spotToleration},
			Containers: []corev1.Container{{
				Name:  "runner",
				Image: "ghcr.io/meshcloud/manual-block-runner:main",
				Env:   []corev1.EnvVar{springProfileKubernetes, logLevel},
			}},
		}), merged)
	})

	t.Run("returns all without a template for the type", func(t *testing.T) {
		merged, err := JobSpecTemplates{RunnerTypeAll: all}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, all, merged)
	})

	t.Run("returns the type without a template for all", func(t *testing.T) {
		merged, err := JobSpecTemplates{RunnerTypeManual: manual}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, manual, merged)
	})

	t.Run("fails without any template", func(t *testing.T) {
		_, err := JobSpecTemplates{RunnerTypeManual: manual}.GetFor(RunnerTypeTf)
		assert.Error(t, err)
	})
}

var (
	springProfileKubernetes = corev1.EnvVar{Name: "SPRING_PROFILES_ACTIVE", Value: "kubernetes"}
	executionModeSingleRun  = corev1.EnvVar{Name: "EXECUTION_MODE", Value: "single-run"}
)

var spotToleration = corev1.Toleration{Key: "spot", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}

func TestJobSpecTemplatesUnmarshalYAML(t *testing.T) {
	t.Run("keys templates by runner type", func(t *testing.T) {
		var templates JobSpecTemplates
		require.NoError(t, templates.UnmarshalYAML([]byte("'*': {}\nmanual: {}\nTERRAFORM: {}\n")))
		assert.Equal(t, JobSpecTemplates{RunnerTypeAll: {}, RunnerTypeManual: {}, RunnerTypeTf: {}}, templates)
	})

	t.Run("fails on an unknown runner type", func(t *testing.T) {
		var templates JobSpecTemplates
		assert.ErrorContains(t, templates.UnmarshalYAML([]byte("unknown: {}\n")), "runner type unknown is unknown")
	})

	t.Run("fails on a runner type set twice", func(t *testing.T) {
		var templates JobSpecTemplates
		assert.ErrorContains(t, templates.UnmarshalYAML([]byte("manual: {}\nMANUAL: {}\n")), "set twice")
	})

	t.Run("fails on an unknown field", func(t *testing.T) {
		var templates JobSpecTemplates
		assert.ErrorContains(t, templates.UnmarshalYAML([]byte("manual: {spec: {unknown: 1}}\n")), `unknown field "unknown"`)
	})
}

func TestValidateK8s(t *testing.T) {
	valid := func() Config {
		return Config{
			Dispatcher: Dispatcher{ForTypes: RunnerTypes{RunnerTypeTf, RunnerTypeManual}},
			K8s: &K8s{
				Namespace: "default",
				JobSpecTemplates: JobSpecTemplates{
					RunnerTypeAll:    runnerContainerTemplate("image"),
					RunnerTypeManual: runnerContainerTemplate("manual-image"),
				},
			},
		}
	}
	assert.NoError(t, valid().ValidateK8s())

	t.Run("fails without k8s", func(t *testing.T) {
		cfg := valid()
		cfg.K8s = nil
		assert.EqualError(t, cfg.ValidateK8s(), "k8s is required for the k8s dispatcher")
	})

	t.Run("fails without namespace and template for a type", func(t *testing.T) {
		cfg := valid()
		cfg.K8s.Namespace = ""
		delete(cfg.K8s.JobSpecTemplates, RunnerTypeAll)
		assert.EqualError(t, cfg.ValidateK8s(), "k8s.namespace is required\nno job spec template for runner type tf")
	})

	t.Run("accepts ALL with an image for one type", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeAll}
		cfg.K8s.JobSpecTemplates[RunnerTypeAll] = runnerContainerTemplate("")
		assert.NoError(t, cfg.ValidateK8s())
	})

	t.Run("fails for ALL without any image", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeAll}
		cfg.K8s.JobSpecTemplates = JobSpecTemplates{RunnerTypeAll: runnerContainerTemplate("")}
		assert.EqualError(t, cfg.ValidateK8s(), "a job spec template needs a container runner with an image")
	})

	t.Run("fails without image for a single type", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeTf}
		cfg.K8s.JobSpecTemplates[RunnerTypeAll] = runnerContainerTemplate("")
		assert.EqualError(t, cfg.ValidateK8s(), "job spec template for runner type tf needs a container runner with an image")
	})
}

func runnerContainerTemplate(image string, env ...corev1.EnvVar) JobSpecTemplate {
	return podSpecTemplate(corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: image, Env: env}}})
}

func podSpecTemplate(spec corev1.PodSpec) JobSpecTemplate {
	return JobSpecTemplate{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: spec}}}
}

func newApiWithWorkspace(apiUrl, workspace string) Api {
	api := newApi(apiUrl, "", "")
	api.Auth.Workspace = workspace
	return api
}
