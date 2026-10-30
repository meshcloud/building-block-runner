variable "terraform_version" {
  type = string
}

variable "repository_url" {
  type = string
}

variable "ref_name" {
  type    = string
  default = null
}

variable "pre_run_script" {
  type    = string
  default = null
}

variable "static_inputs" {
  type    = map(string)
  default = {}
}

variable "wait_for_completion" {
  type    = bool
  default = true
}

variable "purge_on_delete" {
  type    = bool
  default = false
}

variable "approve_manual_triggers" {
  type    = bool
  default = false
}

variable "async" {
  type    = bool
  default = false
}

variable "use_mesh_http_backend_fallback" {
  type    = bool
  default = true
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "tfblock ${var.name}"
    description  = "A tf building block whose implementation the test case configures."
    approval_policies = {
      manual_triggers = var.approve_manual_triggers
    }
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      terraform = {
        repository_url    = var.repository_url
        ref_name          = var.ref_name
        terraform_version = var.terraform_version
        pre_run_script    = var.pre_run_script
        async             = var.async

        use_mesh_http_backend_fallback = var.use_mesh_http_backend_fallback
      }
    }
    inputs = {
      for key, value in var.static_inputs : key => {
        display_name    = key
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode(value)
      }
    }
    outputs = {
      text = {
        display_name    = "Text"
        type            = "STRING"
        assignment_type = "NONE"
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "tfblock ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {}
  }
  wait_for_completion = var.wait_for_completion
  purge_on_delete     = var.purge_on_delete
  timeouts = {
    create = "10m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
