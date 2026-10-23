variable "base_url" {
  type = string
}

variable "app_id" {
  type = string
}

variable "app_private_key" {
  type      = string
  sensitive = true
}

variable "owner" {
  type = string
}

variable "repository" {
  type = string
}

variable "apply_workflow" {
  type = string
}

variable "async" {
  type = bool
}

variable "omit_run_object_input" {
  type = bool
}

variable "wait_for_completion" {
  type    = bool
  default = true
}

variable "secret" {
  type      = string
  sensitive = true
}

resource "meshstack_integration" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "github ${var.name}"
    config = {
      github = {
        owner           = var.owner
        base_url        = var.base_url
        app_id          = var.app_id
        app_private_key = { secret_value = var.app_private_key }
        runner_ref      = meshstack_building_block_runner.this.ref
      }
    }
  }
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "github ${var.name}"
    description  = "Dispatches a workflow of the GitHub mock."
  }
  version_spec = {
    draft         = true
    runner_ref    = meshstack_building_block_runner.this.ref
    deletion_mode = "PURGE"
    implementation = {
      github_workflows = {
        repository            = var.repository
        branch                = "main"
        apply_workflow        = var.apply_workflow
        async                 = var.async
        omit_run_object_input = var.omit_run_object_input
        integration_ref       = { uuid = meshstack_integration.this.metadata.uuid }
      }
    }
    inputs = {
      text = {
        display_name    = "Text"
        type            = "STRING"
        assignment_type = "USER_INPUT"
      }
      secret = {
        display_name    = "Secret"
        type            = "STRING"
        assignment_type = "USER_INPUT"
        sensitive       = {}
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "github ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {
      text   = { value = jsonencode("hello") }
      secret = { sensitive = { secret_value = var.secret } }
    }
  }
  wait_for_completion = var.wait_for_completion
  timeouts = {
    create = "10m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
