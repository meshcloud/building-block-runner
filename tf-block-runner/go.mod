module github.com/meshcloud/meshfed-release/buildingblocks/tf-block-runner

go 1.26

require (
	github.com/go-git/go-git/v5 v5.16.4
	github.com/hashicorp/hc-install v0.9.2
	github.com/hashicorp/hcl/v2 v2.24.0
	github.com/hashicorp/terraform-exec v0.24.0
	github.com/meshcloud/building-block-runner/go-meshapi-client v0.0.0
	github.com/opentofu/tofudl v0.0.1
	github.com/sebdah/goldie/v2 v2.8.0
	github.com/stretchr/testify v1.11.1
)

require (
	dario.cat/mergo v1.0.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/ProtonMail/go-mime v0.0.0-20230322103455-7d82a3887f2f // indirect
	github.com/ProtonMail/gopenpgp/v2 v2.7.5 // indirect
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/cloudflare/circl v1.6.1 // indirect
	github.com/cyphar/filepath-securejoin v0.4.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.6.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/onsi/gomega v1.35.1 // indirect
	github.com/pjbgf/sha1cd v0.3.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sergi/go-diff v1.4.0 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.14.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/tools v0.26.0 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/PaesslerAG/jsonpath v0.1.1
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-version v1.7.0
	github.com/hashicorp/terraform-json v0.27.1 // indirect
	github.com/zclconf/go-cty v1.16.4
	golang.org/x/crypto v0.38.0
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	gopkg.in/yaml.v2 v2.4.0
)

replace github.com/meshcloud/building-block-runner/go-meshapi-client => ../go-meshapi-client
