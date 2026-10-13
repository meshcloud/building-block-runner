package legacy

import "github.com/meshcloud/building-block-runner/internal/crypto"

type RunController struct {
	K8s                    `yaml:",inline"`
	PollingIntervalSeconds int                       `yaml:"pollingIntervalSeconds"`
	MaxConcurrentJobs      int                       `yaml:"maxConcurrentJobs"`
	Uuid                   string                    `yaml:"uuid"`
	OwnedByWorkspace       string                    `yaml:"ownedByWorkspace"`
	DisplayName            string                    `yaml:"displayName"`
	Crypto                 Crypto                    `yaml:"crypto"`
	Implementations        map[string]Implementation `yaml:"implementations"`
}

// Crypto ignores the public key, as config derives it from the private key.
type Crypto struct {
	PrivateKey crypto.PrivateKey `yaml:"privateKey"`
	PublicKey  any               `yaml:"publicKey"`
}
