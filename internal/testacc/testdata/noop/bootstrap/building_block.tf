variable "terraform_version" {
  type = string
}

locals {
  static_inputs = {
    flag              = { type = "BOOLEAN", argument = jsonencode(true) }
    num               = { type = "INTEGER", argument = jsonencode(1) }
    text              = { type = "STRING", argument = jsonencode("hello") }
    single_select     = { type = "STRING", argument = jsonencode("single1") }
    multi_select      = { type = "CODE", argument = jsonencode(jsonencode(["multi1"])) }
    multi_select_json = { type = "STRING", argument = jsonencode(jsonencode(["multi1"])) }
    deploy_settings   = { type = "CODE", argument = jsonencode(jsonencode({ greeting = "hi" })) }
    static            = { type = "STRING", argument = jsonencode("static value") }
    static_code       = { type = "CODE", argument = jsonencode(jsonencode({ some = "code" })) }
    tag_value         = { type = "CODE", argument = jsonencode(jsonencode(["tag"])) }
    operator_text     = { type = "STRING", argument = jsonencode("operator") }
    payment_method    = { type = "CODE", argument = jsonencode(jsonencode({ kind = "meshPaymentMethod", name = "none" })) }
  }
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "hub noop ${var.name}"
    description  = "The noop building block of meshstack-hub, cloned over https."
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      terraform = {
        repository_url    = "https://github.com/meshcloud/meshstack-hub.git"
        repository_path   = "modules/meshstack/noop/buildingblock"
        ref_name          = "main"
        terraform_version = var.terraform_version
      }
    }
    inputs = merge(
      {
        for key, input in local.static_inputs : key => {
          display_name    = key
          type            = input.type
          assignment_type = "STATIC"
          argument        = input.argument
        }
      },
      {
        sensitive_text = {
          display_name    = "sensitive_text"
          type            = "STRING"
          assignment_type = "STATIC"
          sensitive       = { argument = { secret_value = "hidden" } }
        }
        sensitive_yaml = {
          display_name    = "sensitive_yaml"
          type            = "CODE"
          assignment_type = "STATIC"
          sensitive       = { argument = { secret_value = jsonencode({ some = "yaml" }) } }
        }
        user_permissions = {
          display_name    = "user_permissions"
          type            = "CODE"
          assignment_type = "USER_PERMISSIONS"
        }
        user_permissions_json = {
          display_name    = "user_permissions_json"
          type            = "CODE"
          assignment_type = "USER_PERMISSIONS"
        }
        author = {
          display_name    = "author"
          type            = "CODE"
          assignment_type = "AUTHOR"
        }
        workspace_identifier = {
          display_name    = "workspace_identifier"
          type            = "STRING"
          assignment_type = "WORKSPACE_IDENTIFIER"
        }
      },
    )
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
    display_name                          = "hub noop ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {}
  }
  wait_for_completion = true
  timeouts = {
    create = "15m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
