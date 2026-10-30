variable "terraform_version" {
  type = string
}

variable "repository_url" {
  type = string
}

variable "sleep_seconds" {
  type = number
}

variable "approve_manual_triggers" {
  type = bool
}

variable "wait_for_completion" {
  type = bool
}

variable "pre_run_script" {
  type    = string
  default = null
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name     = "sleep ${var.name}"
    description      = "Sleeps while it creates its resource, and plans a change on every run."
    run_transparency = true
    approval_policies = {
      manual_triggers = var.approve_manual_triggers
    }
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      terraform = {
        repository_url                 = var.repository_url
        terraform_version              = var.terraform_version
        use_mesh_http_backend_fallback = true
        pre_run_script                 = var.pre_run_script
      }
    }
    inputs = {
      sleep_seconds = {
        display_name    = "Sleep Seconds"
        type            = "INTEGER"
        assignment_type = "STATIC"
        argument        = jsonencode(var.sleep_seconds)
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "sleep ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {}
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
