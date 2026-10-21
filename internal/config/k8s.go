//go:build k8s

package config

import (
	"encoding/json" //nolint:depguard // strategicpatch merges the encoding/json form of the Kubernetes types
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

type K8s struct {
	Namespace string `yaml:"namespace" env:"NAMESPACE"`
	// JobTtl is nil until ApplyK8sDefaults, as 0 deletes a finished Job at once.
	JobTtl           *xtime.Duration[xtime.Minute] `yaml:"jobTtl" env:"JOB_TTL"`
	JobSpecTemplates JobSpecTemplates              `yaml:"jobSpecTemplates"`
}

const (
	runnerContainerName  = "runner"
	defaultRunnerImage   = "ghcr.io/meshcloud/building-block-runner"
	defaultJobTtlMinutes = 60
)

var podNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

type JobSpecTemplates map[RunnerType]JobSpecTemplate

type JobSpecTemplate = batchv1.JobTemplateSpec

func (ts *JobSpecTemplates) UnmarshalYAML(data []byte) error {
	var byName map[string]JobSpecTemplate
	if err := yaml.UnmarshalWithOptions(data, &byName, yaml.UseJSONUnmarshaler(), yaml.DisallowUnknownField()); err != nil {
		return err
	}
	*ts = make(JobSpecTemplates, len(byName))
	for name, template := range byName {
		runnerType, err := ParseRunnerType(name)
		if err != nil {
			return fmt.Errorf("job spec template: %w", err)
		}
		if _, ok := (*ts)[runnerType]; ok {
			return fmt.Errorf("job spec template for runner type %s is set twice", runnerType)
		}
		(*ts)[runnerType] = template
	}
	return nil
}

func (ts JobSpecTemplates) GetFor(runnerType RunnerType) (JobSpecTemplate, error) {
	all, hasAll := ts[RunnerTypeAll]
	typed, hasTyped := ts[runnerType]
	switch {
	case !hasAll && !hasTyped:
		return JobSpecTemplate{}, fmt.Errorf("no job spec template for runner type %s", runnerType)
	case !hasTyped:
		return all, nil
	case !hasAll:
		return typed, nil
	}

	return merge(all, typed)
}

func merge(base, overlay JobSpecTemplate) (JobSpecTemplate, error) {
	baseJson, err := json.Marshal(base)
	if err != nil {
		return JobSpecTemplate{}, err
	}
	emptyJson, err := json.Marshal(JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	overlayJson, err := json.Marshal(overlay)
	if err != nil {
		return JobSpecTemplate{}, err
	}
	// a patch straight from overlayJson would delete fields of base, as zero fields without omitempty marshal to null
	patch, err := strategicpatch.CreateTwoWayMergePatch(emptyJson, overlayJson, JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	mergedJson, err := strategicpatch.StrategicMergePatch(baseJson, patch, JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	var merged JobSpecTemplate
	return merged, json.Unmarshal(mergedJson, &merged)
}

func (cfg *Config) applyLegacyK8s(rc legacy.RunController) error {
	if rc.Namespace == "" && len(rc.Implementations) == 0 {
		return nil
	}
	byName, err := rc.JobSpecTemplates(string(RunnerTypeAll))
	if err != nil {
		return err
	}
	templates := make(JobSpecTemplates, len(byName))
	for name, template := range byName {
		runnerType, err := ParseRunnerType(name)
		if err != nil {
			return fmt.Errorf("legacy implementation: %w", err)
		}
		templates[runnerType] = template
	}
	cfg.K8s = &K8s{Namespace: rc.Namespace, JobSpecTemplates: templates}
	return nil
}

func (cfg *Config) ApplyK8sDefaults() error {
	if cfg.K8s == nil {
		cfg.K8s = &K8s{}
	}
	if cfg.K8s.Namespace == "" {
		namespace, err := os.ReadFile(podNamespaceFile)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("k8s.namespace is unset and %s does not exist", podNamespaceFile)
		} else if err != nil {
			return fmt.Errorf("namespace of the pod: %w", err)
		}
		cfg.K8s.Namespace = strings.TrimSpace(string(namespace))
	}
	if cfg.K8s.JobTtl == nil {
		cfg.K8s.JobTtl = new(xtime.Minutes(defaultJobTtlMinutes))
	}
	all, err := merge(defaultJobSpecTemplate(), cfg.K8s.JobSpecTemplates[RunnerTypeAll])
	if err != nil {
		return fmt.Errorf("job spec template for runner type %s: %w", RunnerTypeAll, err)
	}
	if cfg.K8s.JobSpecTemplates == nil {
		cfg.K8s.JobSpecTemplates = JobSpecTemplates{}
	}
	cfg.K8s.JobSpecTemplates[RunnerTypeAll] = all
	return nil
}

// defaultJobSpecTemplate leaves the image untagged, so the dispatcher tags it with build.ImageTag.
func defaultJobSpecTemplate() JobSpecTemplate {
	runner := corev1.Container{Name: runnerContainerName}
	if build.ImageTag() != "" {
		runner.Image = defaultRunnerImage
	}
	return JobSpecTemplate{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{runner}}}}}
}

func (cfg *Config) ValidateK8s() error {
	if cfg.K8s == nil {
		return errors.New("k8s is required for the k8s dispatcher")
	}
	var errs []error
	if cfg.K8s.Namespace == "" {
		errs = append(errs, errors.New("k8s.namespace is required"))
	}
	if cfg.K8s.JobTtl != nil && cfg.K8s.JobTtl.Duration < 0 {
		errs = append(errs, errors.New("k8s.jobTtl must not be negative"))
	}
	forAll := slices.Contains(cfg.Dispatcher.ForTypes, RunnerTypeAll)
	runnable := 0
	for _, runnerType := range cfg.Dispatcher.ForTypes.Expand() {
		template, err := cfg.K8s.JobSpecTemplates.GetFor(runnerType)
		switch {
		case err == nil && hasRunnerImage(template):
			runnable++
		case forAll:
			// a run of this type fails when it starts
		case err != nil:
			errs = append(errs, err)
		default:
			errs = append(errs, fmt.Errorf("job spec template for runner type %s needs a container %s with an image", runnerType, runnerContainerName))
		}
	}
	if forAll && runnable == 0 {
		errs = append(errs, fmt.Errorf("a job spec template needs a container %s with an image", runnerContainerName))
	}
	return errors.Join(errs...)
}

func hasRunnerImage(template JobSpecTemplate) bool {
	return slices.ContainsFunc(template.Spec.Template.Spec.Containers, func(container corev1.Container) bool {
		return container.Name == runnerContainerName && container.Image != ""
	})
}
