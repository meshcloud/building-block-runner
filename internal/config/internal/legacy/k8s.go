//go:build k8s

package legacy

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type K8s struct {
	Namespace        string            `yaml:"namespace"`
	ImagePullSecrets []string          `yaml:"imagePullSecrets"`
	Tolerations      []Toleration      `yaml:"tolerations"`
	NodeSelector     map[string]string `yaml:"nodeSelector"`
}

type Toleration struct {
	Key               string `yaml:"key"`
	Operator          string `yaml:"operator"`
	Value             string `yaml:"value"`
	Effect            string `yaml:"effect"`
	TolerationSeconds *int64 `yaml:"tolerationSeconds"`
}

type Implementation struct {
	Image             string             `yaml:"image"`
	Command           []string           `yaml:"command"`
	Args              []string           `yaml:"args"`
	Env               map[string]string  `yaml:"env"`
	Resources         Resources          `yaml:"resources"`
	ExtraVolumes      []ExtraVolume      `yaml:"extraVolumes"`
	ExtraVolumeMounts []ExtraVolumeMount `yaml:"extraVolumeMounts"`
}

type Resources struct {
	Requests Resource `yaml:"requests"`
	Limits   Resource `yaml:"limits"`
}

type Resource struct {
	Cpu    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

type ExtraVolume struct {
	Name      string `yaml:"name"`
	ConfigMap *struct {
		Name string `yaml:"name"`
	} `yaml:"configMap"`
	Secret *struct {
		SecretName string `yaml:"secretName"`
	} `yaml:"secret"`
	EmptyDir *struct {
		SizeLimit string `yaml:"sizeLimit"`
	} `yaml:"emptyDir"`
}

type ExtraVolumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	ReadOnly  bool   `yaml:"readOnly"`
}

func (rc RunController) JobSpecTemplates(allRunnerTypes string) (map[string]batchv1.JobTemplateSpec, error) {
	podSpecTemplate := func(spec corev1.PodSpec) batchv1.JobTemplateSpec {
		return batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: spec}}}
	}
	templates := map[string]batchv1.JobTemplateSpec{}
	if all := rc.podSpec(); !reflect.ValueOf(all).IsZero() {
		templates[allRunnerTypes] = podSpecTemplate(all)
	}
	for runnerType, implementation := range rc.Implementations {
		spec, err := implementation.podSpec()
		if err != nil {
			return nil, fmt.Errorf("legacy implementation %s: %w", runnerType, err)
		}
		templates[runnerType] = podSpecTemplate(spec)
	}
	return templates, nil
}

func (k K8s) podSpec() (spec corev1.PodSpec) {
	for _, t := range k.Tolerations {
		spec.Tolerations = append(spec.Tolerations, corev1.Toleration{
			Key:               t.Key,
			Operator:          corev1.TolerationOperator(t.Operator),
			Value:             t.Value,
			Effect:            corev1.TaintEffect(t.Effect),
			TolerationSeconds: t.TolerationSeconds,
		})
	}
	spec.NodeSelector = k.NodeSelector
	for _, name := range k.ImagePullSecrets {
		spec.ImagePullSecrets = append(spec.ImagePullSecrets, corev1.LocalObjectReference{Name: name})
	}
	return
}

func (implementation Implementation) podSpec() (corev1.PodSpec, error) {
	requests, err := implementation.Resources.Requests.list()
	if err != nil {
		return corev1.PodSpec{}, err
	}
	limits, err := implementation.Resources.Limits.list()
	if err != nil {
		return corev1.PodSpec{}, err
	}
	container := corev1.Container{
		Name:      "runner",
		Image:     implementation.Image,
		Command:   implementation.Command,
		Args:      implementation.Args,
		Resources: corev1.ResourceRequirements{Requests: requests, Limits: limits},
	}
	for _, name := range slices.Sorted(maps.Keys(implementation.Env)) {
		container.Env = append(container.Env, corev1.EnvVar{Name: name, Value: implementation.Env[name]})
	}
	for _, mount := range implementation.ExtraVolumeMounts {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: mount.Name, MountPath: mount.MountPath, ReadOnly: mount.ReadOnly})
	}

	spec := corev1.PodSpec{Containers: []corev1.Container{container}}
	for _, volume := range implementation.ExtraVolumes {
		v, err := volume.volume()
		if err != nil {
			return corev1.PodSpec{}, fmt.Errorf("volume %s: %w", volume.Name, err)
		}
		spec.Volumes = append(spec.Volumes, v)
	}
	return spec, nil
}

func (r Resource) list() (corev1.ResourceList, error) {
	list := corev1.ResourceList{}
	for name, quantity := range map[corev1.ResourceName]string{corev1.ResourceCPU: r.Cpu, corev1.ResourceMemory: r.Memory} {
		if quantity == "" {
			continue
		}
		parsed, err := resource.ParseQuantity(quantity)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		list[name] = parsed
	}
	if len(list) == 0 {
		return nil, nil //nolint:nilnil // a nil list leaves the resources unset
	}
	return list, nil
}

func (volume ExtraVolume) volume() (corev1.Volume, error) {
	v := corev1.Volume{Name: volume.Name}
	switch {
	case volume.ConfigMap != nil:
		v.ConfigMap = &corev1.ConfigMapVolumeSource{Name: volume.ConfigMap.Name}
	case volume.Secret != nil:
		v.Secret = &corev1.SecretVolumeSource{SecretName: volume.Secret.SecretName}
	case volume.EmptyDir != nil:
		v.EmptyDir = &corev1.EmptyDirVolumeSource{}
		if volume.EmptyDir.SizeLimit != "" {
			sizeLimit, err := resource.ParseQuantity(volume.EmptyDir.SizeLimit)
			if err != nil {
				return corev1.Volume{}, err
			}
			v.EmptyDir.SizeLimit = &sizeLimit
		}
	default:
		return corev1.Volume{}, errors.New("needs a configMap, secret or emptyDir")
	}
	return v, nil
}
