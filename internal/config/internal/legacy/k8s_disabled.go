//go:build !k8s

package legacy

// K8s and Implementation only accept the keys, as a binary without k8s support dispatches in process.
type K8s struct {
	Namespace        any `yaml:"namespace"`
	ImagePullSecrets any `yaml:"imagePullSecrets"`
	Tolerations      any `yaml:"tolerations"`
	NodeSelector     any `yaml:"nodeSelector"`
}

type Implementation struct {
	Image             any `yaml:"image"`
	Command           any `yaml:"command"`
	Args              any `yaml:"args"`
	Env               any `yaml:"env"`
	Resources         any `yaml:"resources"`
	ExtraVolumes      any `yaml:"extraVolumes"`
	ExtraVolumeMounts any `yaml:"extraVolumeMounts"`
}
