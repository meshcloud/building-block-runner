//go:build k8s

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/meshcloud/building-block-runner/v2/internal/build"
	"github.com/meshcloud/building-block-runner/v2/internal/config/xtime"
)

func Test_ApplyK8sDefaults_TakesTheNamespaceOfThePod(t *testing.T) {
	file := filepath.Join(t.TempDir(), "namespace")
	withPodNamespaceFile(t, file)

	t.Run("fails without the file", func(t *testing.T) {
		var cfg Config
		assert.EqualError(t, cfg.ApplyK8sDefaults(), "k8s.namespace is unset and "+file+" does not exist")
	})

	require.NoError(t, os.WriteFile(file, []byte("runners\n"), 0o600))

	t.Run("from the file", func(t *testing.T) {
		var cfg Config
		require.NoError(t, cfg.ApplyK8sDefaults())
		assert.Equal(t, "runners", cfg.K8s.Namespace)
	})

	t.Run("configured", func(t *testing.T) {
		cfg := Config{K8s: &K8s{Namespace: "configured"}}
		require.NoError(t, cfg.ApplyK8sDefaults())
		assert.Equal(t, "configured", cfg.K8s.Namespace)
	})
}

func Test_ApplyK8sDefaults_KeepsAFinishedJobForAnHour(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := Config{K8s: &K8s{Namespace: "runners"}}
		require.NoError(t, cfg.ApplyK8sDefaults())
		assert.Equal(t, time.Hour, cfg.K8s.JobTtl.Duration)
	})

	t.Run("configured 0 stays", func(t *testing.T) {
		cfg := Config{K8s: &K8s{Namespace: "runners", JobTtl: new(xtime.Minutes(0))}}
		require.NoError(t, cfg.ApplyK8sDefaults())
		assert.Equal(t, time.Duration(0), cfg.K8s.JobTtl.Duration)
	})

	t.Run("from the environment in minutes without a unit", func(t *testing.T) {
		ensureCleanRunnerEnv(t)
		t.Setenv("RUNNER_K8S_JOB_TTL", "90")
		cfg, err := Load(t.Context(), []byte("k8s:\n  namespace: runners\n"))
		require.NoError(t, err)
		assert.Equal(t, 90*time.Minute, cfg.K8s.JobTtl.Duration)
	})
}

func Test_ApplyK8sDefaults_MergesTheTemplatesOntoTheRunnerImage(t *testing.T) {
	withBuild(t, "v2.0.0", "")
	spot := corev1.PodSpec{NodeSelector: map[string]string{"spot": "true"}}
	templateFor := func(t *testing.T, runnerType RunnerType, templates JobSpecTemplates) JobSpecTemplate {
		t.Helper()
		cfg := Config{K8s: &K8s{Namespace: "runners", JobSpecTemplates: templates}}
		require.NoError(t, cfg.ApplyK8sDefaults())
		template, err := cfg.K8s.JobSpecTemplates.GetFor(runnerType)
		require.NoError(t, err)
		return template
	}

	t.Run("without templates", func(t *testing.T) {
		assert.Equal(t, runnerContainerTemplate("ghcr.io/meshcloud/building-block-runner"), templateFor(t, RunnerTypeAll, nil))
	})

	t.Run("ALL keeps the default image", func(t *testing.T) {
		want := podSpecTemplate(spot)
		want.Spec.Template.Spec.Containers = []corev1.Container{{Name: "runner", Image: "ghcr.io/meshcloud/building-block-runner"}}
		assert.Equal(t, want, templateFor(t, RunnerTypeAll, JobSpecTemplates{RunnerTypeAll: podSpecTemplate(spot)}))
	})

	t.Run("ALL overrides the image", func(t *testing.T) {
		assert.Equal(t, runnerContainerTemplate("own"), templateFor(t, RunnerTypeAll, JobSpecTemplates{RunnerTypeAll: runnerContainerTemplate("own")}))
	})

	t.Run("a typed template merges onto the default and ALL", func(t *testing.T) {
		want := podSpecTemplate(spot)
		want.Spec.Template.Spec.Containers = []corev1.Container{{Name: "runner", Image: "ghcr.io/meshcloud/building-block-runner", Env: []corev1.EnvVar{logLevelDebug}}}
		templates := JobSpecTemplates{RunnerTypeAll: podSpecTemplate(spot), RunnerTypeTf: runnerContainerTemplate("", logLevelDebug)}
		assert.Equal(t, want, templateFor(t, RunnerTypeTf, templates))
	})

	t.Run("without a version and a revision, a template must set the image", func(t *testing.T) {
		withBuild(t, "dev", "")
		cfg := Config{Dispatcher: Dispatcher{ForTypes: RunnerTypes{RunnerTypeAll}}, K8s: &K8s{Namespace: "runners"}}
		require.NoError(t, cfg.ApplyK8sDefaults())
		assert.EqualError(t, cfg.ValidateK8s(), "a job spec template needs a container runner with an image")
	})
}

func withBuild(t *testing.T, version, revision string) {
	t.Helper()
	originalVersion, originalRevision := build.Version, build.Revision
	t.Cleanup(func() { build.Version, build.Revision = originalVersion, originalRevision })
	build.Version, build.Revision = version, revision
}

func withPodNamespaceFile(t *testing.T, file string) {
	t.Helper()
	original := podNamespaceFile
	t.Cleanup(func() { podNamespaceFile = original })
	podNamespaceFile = file
}
