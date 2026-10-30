variable "text" {
  type = string
}

variable "num" {
  type = number
}

variable "flag" {
  type = bool
}

variable "single_select" {
  type = string
}

variable "multi_select" {
  type = list(string)
}

variable "code" {
  type = map(string)
}

variable "settings" {
  type = object({ greeting = string })
}

variable "optional_text" {
  type    = string
  default = "tf default"
}

variable "conditional_text" {
  type    = string
  default = "tf default"
}

variable "secret" {
  type      = string
  sensitive = true
}

variable "expected_secret_sha256" {
  type = string
}

variable "static" {
  type = string
}

variable "static_secret" {
  type      = string
  sensitive = true
}

variable "expected_workspace" {
  type = string
}

variable "tag" {
  type = list(string)
}

variable "user_permissions" {
  type = list(object({
    username = string
    roles    = list(string)
  }))
}

variable "author" {
  type = object({
    type        = string
    identifier  = string
    displayName = string
  })
}

variable "workspace" {
  type = string
}

variable "env_var" {
  type = string
}

variable "num_text" {
  type = string
}

variable "flag_text" {
  type = string
}

variable "multi_select_text" {
  type = string
}

variable "code_text" {
  type = string
}

variable "code_hcl" {
  type = map(string)
}

variable "code_yaml" {
  type = string
}

variable "meshstack_building_block_id" {
  type = string
}

resource "terraform_data" "inputs" {
  input = file("greeting.txt")

  lifecycle {
    postcondition {
      condition     = var.text == "hello"
      error_message = "STRING input text is not hello."
    }
    postcondition {
      condition     = var.num == 42
      error_message = "INTEGER input num is not 42."
    }
    postcondition {
      condition     = var.flag
      error_message = "BOOLEAN input flag is not true."
    }
    postcondition {
      condition     = var.single_select == "b"
      error_message = "SINGLE_SELECT input single_select is not b."
    }
    postcondition {
      condition     = var.multi_select == tolist(["a", "c"])
      error_message = "MULTI_SELECT input multi_select is not [a, c]."
    }
    postcondition {
      condition     = var.code == tomap({ key = "value" })
      error_message = "CODE input code is not {key = value}."
    }
    postcondition {
      condition     = var.settings.greeting == "hi"
      error_message = "JSON input settings has no greeting hi."
    }
    postcondition {
      condition     = var.optional_text == "tf default"
      error_message = "optional input optional_text did not fall back to the tf default."
    }
    postcondition {
      condition     = var.conditional_text == "tf default"
      error_message = "conditional input conditional_text did not fall back to the tf default."
    }
    postcondition {
      condition     = sha256(var.secret) == var.expected_secret_sha256
      error_message = "sensitive input secret does not hold the expected secret."
    }
    postcondition {
      condition     = var.static == "static value"
      error_message = "STATIC input static is not static value."
    }
    postcondition {
      condition     = var.static_secret == "static secret"
      error_message = "sensitive STATIC input static_secret is not static secret."
    }
    postcondition {
      condition     = self.output == "hello file\n"
      error_message = "FILE input greeting.txt does not hold hello file."
    }
    postcondition {
      condition     = var.tag == tolist(["unit-a"])
      error_message = "TAG input tag is not [unit-a]."
    }
    postcondition {
      condition     = contains(var.user_permissions[*].username, "customer@meshcloud.io")
      error_message = "USER_PERMISSIONS input user_permissions lacks the workspace member."
    }
    postcondition {
      condition     = var.author.identifier != ""
      error_message = "AUTHOR input author has no identifier."
    }
    postcondition {
      condition     = var.workspace == var.expected_workspace
      error_message = "WORKSPACE_IDENTIFIER input workspace is not the workspace of the building block."
    }
    postcondition {
      condition     = var.env_var == "from env"
      error_message = "environment input TF_VAR_env_var is not from env."
    }
    postcondition {
      condition     = var.num_text == "42"
      error_message = "INTEGER input num_text is not the string 42."
    }
    postcondition {
      condition     = var.flag_text == "true"
      error_message = "BOOLEAN input flag_text is not the string true."
    }
    postcondition {
      condition     = var.multi_select_text == "[\"a\",\"c\"]"
      error_message = "MULTI_SELECT input multi_select_text is not the JSON text [\"a\",\"c\"]."
    }
    postcondition {
      condition     = var.code_text == "{\"key\":\"value\"}"
      error_message = "CODE input code_text is not the JSON text {\"key\":\"value\"}."
    }
    postcondition {
      condition     = var.code_hcl == tomap({ key = "value" })
      error_message = "CODE input code_hcl in HCL is not {key = value}."
    }
    postcondition {
      condition     = var.code_yaml == "key: value"
      error_message = "CODE input code_yaml in YAML is not the text key: value."
    }
  }
}

locals {
  run = jsondecode(base64decode(var.meshstack_building_block_run_b64))
}

output "text" {
  value = var.text
}

output "author" {
  value = var.author
}

output "number" {
  value = var.num
}

output "bool" {
  value = var.flag
}

output "list" {
  value = var.multi_select
}

output "object" {
  value = var.settings
}

output "sensitive_secret" {
  value     = var.secret
  sensitive = true
}

output "run_uuid" {
  value = local.run.metadata.uuid
}

output "building_block_uuid" {
  value = var.meshstack_building_block_id
}

output "run_implementation" {
  value = local.run.spec.buildingBlockDefinition.spec.implementation
}
