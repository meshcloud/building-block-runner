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
  }
}

output "text" {
  value = var.text
}

output "author" {
  value = var.author
}
