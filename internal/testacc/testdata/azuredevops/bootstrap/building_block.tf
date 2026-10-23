variable "base_url" {
  type = string
}

variable "organization" {
  type = string
}

variable "project" {
  type = string
}

variable "pipeline_id" {
  type = string
}

variable "pat" {
  type      = string
  sensitive = true
}

variable "async" {
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
    display_name = "azuredevops ${var.name}"
    config = {
      azuredevops = {
        base_url              = var.base_url
        organization          = var.organization
        personal_access_token = { secret_value = var.pat }
        runner_ref            = meshstack_building_block_runner.this.ref
      }
    }
  }
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "azuredevops ${var.name}"
    description  = "Runs a pipeline of the Azure DevOps mock."
  }
  version_spec = {
    draft         = true
    runner_ref    = meshstack_building_block_runner.this.ref
    deletion_mode = "PURGE"
    implementation = {
      azure_devops_pipeline = {
        project         = var.project
        pipeline_id     = var.pipeline_id
        ref_name        = "refs/heads/main"
        async           = var.async
        integration_ref = { uuid = meshstack_integration.this.metadata.uuid }
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
    display_name                          = "azuredevops ${var.name}"
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
