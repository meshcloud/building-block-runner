terraform {
  required_providers {
    meshstack = {
      source = "meshcloud/meshstack"
    }
  }
}

variable "name" {
  type = string
}

variable "public_key" {
  type = string
}

variable "runner_implementation_type" {
  type = string
}

variable "runner_registers" {
  type = bool
}

resource "meshstack_workspace" "this" {
  metadata = {
    name = var.name
  }
  spec = {
    display_name = "building-block-runner TestAcc ${var.name}"
  }
}

resource "meshstack_building_block_runner" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name        = "building-block-runner TestAcc ${var.name}"
    implementation_type = var.runner_implementation_type
    public_key          = var.public_key
    restriction         = "PRIVATE"
  }
  # A runner that registers updates its spec.
  lifecycle {
    ignore_changes = [spec]
  }
}

resource "meshstack_api_key" "runner" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "building-block-runner TestAcc ${var.name}"
    permissions = concat(
      ["MANAGED_BUILDINGBLOCKRUN_SAVE", "MANAGED_BUILDINGBLOCKRUNSOURCE_SAVE"],
      var.runner_registers ? ["BUILDINGBLOCKRUNNER_SAVE"] : [],
    )
  }
}

output "runner_api_key" {
  value     = meshstack_api_key.runner.status
  sensitive = true
}

output "runner_uuid" {
  value = meshstack_building_block_runner.this.metadata.uuid
}
