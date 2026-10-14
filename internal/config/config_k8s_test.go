//go:build k8s

package config

import (
	"maps"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

var (
	springProfileKubernetes = corev1.EnvVar{Name: "SPRING_PROFILES_ACTIVE", Value: "kubernetes"}
	logLevelDebug           = corev1.EnvVar{Name: "LOG_LEVEL", Value: "debug"}
	spotToleration          = corev1.Toleration{Key: "spot", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}
	typeTemplates           = JobSpecTemplates{
		RunnerTypeTf:          runnerContainerTemplate("ghcr.io/meshcloud/tf-block-runner:main", corev1.EnvVar{Name: "EXECUTION_MODE", Value: "single-run"}),
		RunnerTypeManual:      runnerContainerTemplate("ghcr.io/meshcloud/manual-block-runner:main", springProfileKubernetes),
		RunnerTypeGitHub:      runnerContainerTemplate("ghcr.io/meshcloud/github-block-runner:main", springProfileKubernetes),
		RunnerTypeGitLab:      runnerContainerTemplate("ghcr.io/meshcloud/gitlab-block-runner:main", springProfileKubernetes),
		RunnerTypeAzureDevOps: runnerContainerTemplate("ghcr.io/meshcloud/azure-devops-block-runner:main", springProfileKubernetes),
	}
)

func Test_LoadK8s_ReadsTheNewAndTheLegacyLayouts(t *testing.T) {
	unsetRunnerEnv(t)

	t.Run("new layout", func(t *testing.T) {
		api := newApi("https://host.minikube.internal:8080", "your-client-id", "your-client-secret")
		api.Auth.Profile, api.Auth.Workspace = "local-dev", "admin-customer"
		templates := maps.Clone(typeTemplates)
		templates[RunnerTypeAll] = podSpecTemplate(corev1.PodSpec{
			Tolerations:  []corev1.Toleration{spotToleration},
			NodeSelector: map[string]string{"spot": "true"},
			Containers:   []corev1.Container{{Name: "runner", Env: []corev1.EnvVar{logLevelDebug}}},
		})

		tfConfig := tf.Default()
		tfConfig.CacheDir = "/tmp/runner/tfcache"
		tfConfig.StopGrace = xtime.Seconds(90)

		cfg := loadFile(t, "runner-config-new.yml")

		assert.Equal(t, Config{
			Api:    api,
			Crypto: crypto.Crypto{PrivateKeyFiles: crypto.PrivateKeyFiles{{Path: "./testdata/private-key.pem", PrivateKey: parsedPrivateKey(t)}}},
			Dispatcher: Dispatcher{
				ForTypes:          RunnerTypes{RunnerTypeAll},
				Register:          &Register{DisplayName: "meshcloud-hosted Runner"},
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(45),
				KillGrace:         xtime.Seconds(150),
			},
			K8s: &K8s{Namespace: "default", JobSpecTemplates: templates},
			Tf:  tfConfig,
		}, cfg)
	})

	t.Run("legacy k8s layout", func(t *testing.T) {
		api := newApi("https://host.minikube.internal:8080", "", "")
		api.Auth.Workspace = "admin-customer"

		cfg := loadFile(t, "runner-config-old-k8s.yml")

		assert.Equal(t, Config{
			Uuid:   uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f"),
			Api:    api,
			Crypto: crypto.Crypto{PrivateKeys: crypto.PrivateKeys{parsedPrivateKey(t)}},
			Dispatcher: Dispatcher{
				ForTypes:          RunnerTypes{RunnerTypeAll},
				Register:          &Register{DisplayName: "meshcloud-hosted Runner"},
				PollingInterval:   xtime.Seconds(10),
				MaxConcurrentJobs: 20,
				Timeout:           xtime.Minutes(60),
				KillGrace:         xtime.Seconds(120),
			},
			K8s: &K8s{Namespace: "default", JobSpecTemplates: typeTemplates},
			Tf:  defaultTf(uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")),
		}, cfg)
	})
}

func Test_JobSpecTemplates_MergeTheRunnerTypeIntoAll(t *testing.T) {
	all := podSpecTemplate(corev1.PodSpec{
		Tolerations: []corev1.Toleration{spotToleration},
		Containers:  []corev1.Container{{Name: "runner", Env: []corev1.EnvVar{logLevelDebug}}},
	})
	manual := typeTemplates[RunnerTypeManual]

	t.Run("GetFor merges the type into all", func(t *testing.T) {
		merged, err := JobSpecTemplates{RunnerTypeAll: all, RunnerTypeManual: manual}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, podSpecTemplate(corev1.PodSpec{
			Tolerations: []corev1.Toleration{spotToleration},
			Containers: []corev1.Container{{
				Name:  "runner",
				Image: "ghcr.io/meshcloud/manual-block-runner:main",
				Env:   []corev1.EnvVar{springProfileKubernetes, logLevelDebug},
			}},
		}), merged)
	})

	t.Run("GetFor falls back to the one template there is", func(t *testing.T) {
		merged, err := JobSpecTemplates{RunnerTypeAll: all}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, all, merged)
		merged, err = JobSpecTemplates{RunnerTypeManual: manual}.GetFor(RunnerTypeManual)
		require.NoError(t, err)
		assert.Equal(t, manual, merged)
		_, err = JobSpecTemplates{RunnerTypeManual: manual}.GetFor(RunnerTypeTf)
		assert.Error(t, err)
	})

	t.Run("UnmarshalYAML keys templates by runner type", func(t *testing.T) {
		var templates JobSpecTemplates
		require.NoError(t, templates.UnmarshalYAML([]byte("'*': {}\nmanual: {}\nTERRAFORM: {}\n")))
		assert.Equal(t, JobSpecTemplates{RunnerTypeAll: {}, RunnerTypeManual: {}, RunnerTypeTf: {}}, templates)
	})

	for yml, expectedErr := range map[string]string{
		"unknown: {}\n":                  "runner type unknown is unknown",
		"manual: {}\nMANUAL: {}\n":       "set twice",
		"manual: {spec: {unknown: 1}}\n": `unknown field "unknown"`,
	} {
		t.Run("UnmarshalYAML fails on "+expectedErr, func(t *testing.T) {
			var templates JobSpecTemplates
			assert.ErrorContains(t, templates.UnmarshalYAML([]byte(yml)), expectedErr)
		})
	}
}

func Test_ValidateK8s_NeedsANamespaceAndAnImagePerType(t *testing.T) {
	valid := func() Config {
		return Config{
			Dispatcher: Dispatcher{ForTypes: RunnerTypes{RunnerTypeTf, RunnerTypeManual}},
			K8s: &K8s{Namespace: "default", JobSpecTemplates: JobSpecTemplates{
				RunnerTypeAll:    runnerContainerTemplate("image"),
				RunnerTypeManual: runnerContainerTemplate("manual-image"),
			}},
		}
	}
	require.NoError(t, new(valid()).ValidateK8s())

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

	t.Run("ALL needs an image for at least one type", func(t *testing.T) {
		cfg := valid()
		cfg.Dispatcher.ForTypes = RunnerTypes{RunnerTypeAll}
		cfg.K8s.JobSpecTemplates[RunnerTypeAll] = runnerContainerTemplate("")
		require.NoError(t, cfg.ValidateK8s())
		delete(cfg.K8s.JobSpecTemplates, RunnerTypeManual)
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
