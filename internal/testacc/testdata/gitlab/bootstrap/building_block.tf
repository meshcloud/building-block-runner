variable "base_url" {
  type = string
}

variable "project_id" {
  type = string
}

variable "trigger_token" {
  type      = string
  sensitive = true
}

variable "secret" {
  type      = string
  sensitive = true
}

variable "deletion_mode" {
  type    = string
  default = "PURGE"
}

resource "meshstack_integration" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "gitlab ${var.name}"
    config = {
      gitlab = {
        base_url   = var.base_url
        runner_ref = meshstack_building_block_runner.this.ref
      }
    }
  }
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "gitlab ${var.name}"
    description  = "Triggers a pipeline of the GitLab mock."
  }
  version_spec = {
    draft         = true
    runner_ref    = meshstack_building_block_runner.this.ref
    deletion_mode = var.deletion_mode
    implementation = {
      gitlab_pipeline = {
        project_id             = var.project_id
        ref_name               = "main"
        pipeline_trigger_token = { secret_value = var.trigger_token }
        integration_ref        = { uuid = meshstack_integration.this.metadata.uuid }
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
      num = {
        display_name    = "Number"
        type            = "INTEGER"
        assignment_type = "USER_INPUT"
      }
      flag = {
        display_name    = "Flag"
        type            = "BOOLEAN"
        assignment_type = "USER_INPUT"
      }
      multi_select = {
        display_name      = "Multi Select"
        type              = "MULTI_SELECT"
        assignment_type   = "USER_INPUT"
        selectable_values = ["a", "b", "c"]
      }
      ENV_TEXT = {
        display_name    = "Env Text"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode("hello env")
        is_environment  = true
      }
      ENV_FLAG = {
        display_name    = "Env Flag"
        type            = "BOOLEAN"
        assignment_type = "STATIC"
        argument        = jsonencode(true)
        is_environment  = true
      }
      ENV_MULTI_SELECT = {
        display_name      = "Env Multi Select"
        type              = "MULTI_SELECT"
        assignment_type   = "USER_INPUT"
        selectable_values = ["a", "b", "c"]
        is_environment    = true
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "gitlab ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {
      text             = { value = jsonencode("hello") }
      num              = { value = jsonencode(42) }
      flag             = { value = jsonencode(true) }
      multi_select     = { value = jsonencode(["a", "c"]) }
      ENV_MULTI_SELECT = { value = jsonencode(["a", "c"]) }
      secret           = { sensitive = { secret_value = var.secret } }
    }
  }
  wait_for_completion = true
  timeouts = {
    create = "10m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
