variable "pipeline" {
  type = string

  validation {
    condition     = contains(["gitlab", "github", "azuredevops"], var.pipeline)
    error_message = "pipeline must be gitlab, github or azuredevops."
  }
}

variable "trigger_url" {
  type = string
}

variable "trigger_token" {
  type = string
}

variable "meshstack_building_block_run_b64" {
  type = string
}

locals {
  run = jsondecode(base64decode(var.meshstack_building_block_run_b64))
}

output "text" {
  value = "triggered"
}
