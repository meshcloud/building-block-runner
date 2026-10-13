//go:build k8s

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/goccy/go-yaml"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
)

type K8s struct {
	Namespace        string           `yaml:"namespace" env:"NAMESPACE"`
	JobSpecTemplates JobSpecTemplates `yaml:"jobSpecTemplates"`
}

const runnerContainerName = "runner"

// JobSpecTemplates are keyed by runner type, and RunnerTypeAll holds the template that all others are merged into.
type JobSpecTemplates map[RunnerType]JobSpecTemplate

// still pick up simplification: top-level image|env -> containers[0].image|env
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

	allJson, err := json.Marshal(all)
	if err != nil {
		return JobSpecTemplate{}, err
	}
	emptyJson, err := json.Marshal(JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	typedJson, err := json.Marshal(typed)
	if err != nil {
		return JobSpecTemplate{}, err
	}
	// a patch straight from typedJson would delete fields of all, as zero fields without omitempty marshal to null
	patch, err := strategicpatch.CreateTwoWayMergePatch(emptyJson, typedJson, JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	mergedJson, err := strategicpatch.StrategicMergePatch(allJson, patch, JobSpecTemplate{})
	if err != nil {
		return JobSpecTemplate{}, err
	}
	var merged JobSpecTemplate
	return merged, json.Unmarshal(mergedJson, &merged)
}

func k8sFromLegacy(rc legacy.RunController) (*K8s, error) {
	if rc.Namespace == "" && len(rc.Implementations) == 0 {
		return nil, nil
	}
	byName, err := rc.JobSpecTemplates(string(RunnerTypeAll))
	if err != nil {
		return nil, err
	}
	templates := make(JobSpecTemplates, len(byName))
	for name, template := range byName {
		runnerType, err := ParseRunnerType(name)
		if err != nil {
			return nil, fmt.Errorf("legacy implementation: %w", err)
		}
		templates[runnerType] = template
	}
	return &K8s{Namespace: rc.Namespace, JobSpecTemplates: templates}, nil
}

// ValidateK8s checks what the k8s dispatcher needs on top of Validate.
func (cfg Config) ValidateK8s() error {
	if cfg.K8s == nil {
		return errors.New("k8s is required for the k8s dispatcher")
	}
	var errs []error
	if cfg.K8s.Namespace == "" {
		errs = append(errs, errors.New("k8s.namespace is required"))
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
