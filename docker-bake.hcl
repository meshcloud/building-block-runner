# docker-bake.hcl — the single source of truth for every shipped image, driven by `task images`
# (which invokes the standalone `docker-buildx bake` binary). Each target builds ./cmd/bbrunner
# via the root Dockerfile's CMD_TAGS build-arg; the four HTTP fit images + run-controller share
# the scratch-runtime stage, tf uses tf-runtime.
#
# Published image names are frozen (customer-referenced): manual-block-runner,
# github-block-runner, gitlab-block-runner, azure-devops-block-runner, tf-block-runner,
# run-controller.
#
# Tag/registry matrix: REGISTRIES x TAGS (both comma-separated), so the same bake preserves
# today's dual push to ghcr.io AND docker.io. Defaults produce a single ghcr.io/meshcloud/<name>:dev
# local tag. CI overrides via env (REGISTRIES, TAGS, VERSION) and adds `--push`; push=true only on
# main/tags, false on PRs. VERSION is baked into the binary via the ldflags build-arg.

variable "REGISTRIES" {
  default = "ghcr.io/meshcloud"
}

variable "TAGS" {
  default = "dev"
}

variable "VERSION" {
  default = "dev"
}

# PLATFORMS is empty by default so a local `task images` builds host-native on the plain docker
# driver (which cannot do multi-platform). CI sets PLATFORMS="linux/amd64,linux/arm64" together
# with a docker-container/containerd builder to produce the published multi-arch images.
variable "PLATFORMS" {
  default = ""
}

# imagetags builds the full REGISTRIES x TAGS cross product for one image name.
function "imagetags" {
  params = [name]
  result = flatten([
    for reg in split(",", REGISTRIES) : [
      for t in split(",", TAGS) : "${reg}/${name}:${t}"
    ]
  ])
}

group "default" {
  targets = [
    "manual",
    "github",
    "gitlab",
    "azdevops",
    "tf",
    "run-controller",
  ]
}

# Common settings shared by every target.
target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
  args = {
    VERSION = "${VERSION}"
  }
  platforms = PLATFORMS != "" ? split(",", PLATFORMS) : []
}

target "manual" {
  inherits = ["_common"]
  target   = "scratch-runtime"
  args = {
    CMD_TAGS    = "type_manual"
    CONFIG_FILE = "cmd/runner-config.yml"
  }
  tags = imagetags("manual-block-runner")
}

target "github" {
  inherits = ["_common"]
  target   = "scratch-runtime"
  args = {
    CMD_TAGS    = "type_github"
    CONFIG_FILE = "cmd/runner-config.yml"
  }
  tags = imagetags("github-block-runner")
}

target "gitlab" {
  inherits = ["_common"]
  target   = "scratch-runtime"
  args = {
    CMD_TAGS    = "type_gitlab"
    CONFIG_FILE = "cmd/runner-config.yml"
  }
  tags = imagetags("gitlab-block-runner")
}

target "azdevops" {
  inherits = ["_common"]
  target   = "scratch-runtime"
  args = {
    CMD_TAGS    = "type_azdevops"
    CONFIG_FILE = "cmd/runner-config.yml"
  }
  tags = imagetags("azure-devops-block-runner")
}

# tf uses the alpine tf-runtime stage; its config + known_hosts are baked in the stage.
target "tf" {
  inherits = ["_common"]
  target   = "tf-runtime"
  args = {
    CMD_TAGS = "type_tf"
  }
  tags = imagetags("tf-block-runner")
}

# run-controller: -tags k8s (Kubernetes-Job dispatcher, no in-process handlers), its own config.
target "run-controller" {
  inherits = ["_common"]
  target   = "scratch-runtime"
  args = {
    CMD_TAGS    = "k8s"
    CONFIG_FILE = "cmd/bbrunner/runner-config.yml"
  }
  tags = imagetags("run-controller")
}
