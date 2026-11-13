variable "terraform_version" {
  type = string
}

variable "repository_url" {
  type = string
}

variable "secret" {
  type      = string
  sensitive = true
}

variable "expected_secret" {
  type      = string
  sensitive = true
}

variable "env_secret" {
  type      = string
  sensitive = true
}

resource "meshstack_tag_definition" "unit" {
  spec = {
    target_kind  = "meshWorkspace"
    key          = "${var.name}-unit"
    display_name = "Unit ${var.name}"
    value_type   = { string = {} }
  }
}

resource "meshstack_workspace_tag" "unit" {
  metadata = {
    workspace_identifier = meshstack_workspace.this.metadata.name
    key                  = meshstack_tag_definition.unit.spec.key
  }
  spec = {
    values = ["unit-a"]
  }
}

resource "meshstack_workspace_user_binding" "member" {
  metadata = {
    name = "${var.name}-member"
  }
  role_ref = {
    name = "Workspace Member"
  }
  target_ref = {
    name = meshstack_workspace.this.metadata.name
  }
  subject = {
    name = "customer@meshcloud.io"
  }
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "inputs ${var.name}"
    description  = "Checks every input type the tf runner passes on."
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      terraform = {
        repository_url    = var.repository_url
        terraform_version = var.terraform_version
        pre_run_script    = file("${path.module}/pre_run.sh")
      }
    }
    inputs = {
      text = {
        display_name    = "Text"
        type            = "STRING"
        assignment_type = "USER_INPUT"
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
      single_select = {
        display_name      = "Single Select"
        type              = "SINGLE_SELECT"
        assignment_type   = "USER_INPUT"
        selectable_values = ["a", "b"]
      }
      multi_select = {
        display_name      = "Multi Select"
        type              = "MULTI_SELECT"
        assignment_type   = "USER_INPUT"
        selectable_values = ["a", "b", "c"]
      }
      code = {
        display_name    = "Code"
        type            = "CODE"
        assignment_type = "USER_INPUT"
      }
      settings = {
        display_name    = "Settings"
        type            = "JSON"
        assignment_type = "USER_INPUT"
        json_schema = jsonencode({
          type       = "object"
          required   = ["greeting"]
          properties = { greeting = { type = "string" } }
        })
      }
      optional_text = {
        display_name    = "Optional Text"
        type            = "STRING"
        assignment_type = "USER_INPUT"
        is_optional     = true
      }
      conditional_text = {
        display_name    = "Conditional Text"
        type            = "STRING"
        assignment_type = "USER_INPUT"
        condition       = "input.flag == false"
      }
      secret = {
        display_name    = "Secret"
        type            = "STRING"
        assignment_type = "USER_INPUT"
        sensitive       = {}
      }
      static = {
        display_name    = "Static"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode("static value")
      }
      static_secret = {
        display_name    = "Static Secret"
        type            = "STRING"
        assignment_type = "STATIC"
        sensitive = {
          argument = {
            secret_value = "static secret"
          }
        }
      }
      expected_secret_sha256 = {
        display_name    = "Expected Secret SHA-256"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode(nonsensitive(sha256(var.expected_secret)))
      }
      expected_workspace = {
        display_name    = "Expected Workspace"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode(var.name)
      }
      "greeting.txt" = {
        display_name    = "File"
        type            = "FILE"
        assignment_type = "STATIC"
        argument        = jsonencode("data:text/plain;base64,${base64encode("hello file\n")}")
      }
      tag = {
        display_name    = "Tag"
        type            = "CODE"
        assignment_type = "TAG"
        argument        = jsonencode("WORKSPACE.${meshstack_tag_definition.unit.spec.key}")
      }
      user_permissions = {
        display_name    = "User Permissions"
        type            = "CODE"
        assignment_type = "USER_PERMISSIONS"
      }
      author = {
        display_name    = "Author"
        type            = "CODE"
        assignment_type = "AUTHOR"
      }
      workspace = {
        display_name    = "Workspace"
        type            = "STRING"
        assignment_type = "WORKSPACE_IDENTIFIER"
      }
      num_text = {
        display_name    = "Number as Text"
        type            = "INTEGER"
        assignment_type = "STATIC"
        argument        = jsonencode(42)
      }
      flag_text = {
        display_name    = "Flag as Text"
        type            = "BOOLEAN"
        assignment_type = "STATIC"
        argument        = jsonencode(true)
      }
      multi_select_text = {
        display_name      = "Multi Select as Text"
        type              = "MULTI_SELECT"
        assignment_type   = "USER_INPUT"
        selectable_values = ["a", "b", "c"]
      }
      code_text = {
        display_name    = "Code as Text"
        type            = "CODE"
        assignment_type = "STATIC"
        argument        = jsonencode(jsonencode({ key = "value" }))
      }
      code_hcl = {
        display_name    = "Code in HCL"
        type            = "CODE"
        assignment_type = "STATIC"
        argument        = jsonencode("{ key = \"value\" }")
      }
      code_yaml = {
        display_name    = "Code in YAML"
        type            = "CODE"
        assignment_type = "STATIC"
        argument        = jsonencode("key: value")
      }
      ENV_TEXT = {
        display_name    = "Env Text"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode("hello env")
        is_environment  = true
      }
      ENV_NUM = {
        display_name    = "Env Number"
        type            = "INTEGER"
        assignment_type = "STATIC"
        argument        = jsonencode(42)
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
      ENV_CODE = {
        display_name    = "Env Code"
        type            = "CODE"
        assignment_type = "STATIC"
        argument        = jsonencode(jsonencode({ key = "value" }))
        is_environment  = true
      }
      ENV_SECRET = {
        display_name    = "Env Secret"
        type            = "STRING"
        assignment_type = "USER_INPUT"
        sensitive       = {}
        is_environment  = true
      }
      ENV_SECRET_SHA256 = {
        display_name    = "Env Secret SHA-256"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode(nonsensitive(sha256(var.env_secret)))
        is_environment  = true
      }
      TF_VAR_env_var = {
        display_name    = "TF_VAR_ Env"
        type            = "STRING"
        assignment_type = "STATIC"
        argument        = jsonencode("from env")
        is_environment  = true
      }
    }
    outputs = {
      text = {
        display_name    = "Text"
        type            = "STRING"
        assignment_type = "NONE"
      }
      number = {
        display_name    = "Number"
        type            = "INTEGER"
        assignment_type = "NONE"
      }
      bool = {
        display_name    = "Bool"
        type            = "BOOLEAN"
        assignment_type = "NONE"
      }
      list = {
        display_name    = "List"
        type            = "CODE"
        assignment_type = "NONE"
      }
      object = {
        display_name    = "Object"
        type            = "CODE"
        assignment_type = "NONE"
      }
      run_uuid = {
        display_name    = "Run UUID"
        type            = "STRING"
        assignment_type = "NONE"
      }
      building_block_uuid = {
        display_name    = "Building Block UUID"
        type            = "STRING"
        assignment_type = "NONE"
      }
      run_implementation = {
        display_name    = "Run Implementation"
        type            = "CODE"
        assignment_type = "NONE"
      }
      sensitive_secret = {
        display_name    = "Sensitive Secret"
        type            = "STRING"
        assignment_type = "NONE"
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  depends_on = [meshstack_workspace_tag.unit, meshstack_workspace_user_binding.member]

  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "inputs ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {
      text              = { value = jsonencode("hello") }
      num               = { value = jsonencode(42) }
      flag              = { value = jsonencode(true) }
      single_select     = { value = jsonencode("b") }
      multi_select      = { value = jsonencode(["a", "c"]) }
      ENV_MULTI_SELECT  = { value = jsonencode(["a", "c"]) }
      multi_select_text = { value = jsonencode(["a", "c"]) }
      code              = { value = jsonencode(jsonencode({ key = "value" })) }
      settings          = { value = jsonencode(jsonencode({ greeting = "hi" })) }
      secret            = { sensitive = { secret_value = var.secret } }
      ENV_SECRET        = { sensitive = { secret_value = var.env_secret } }
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
