variable "terraform_version" {
  type = string
}

variable "repository_url" {
  type = string
}

variable "ssh_private_key" {
  type      = string
  sensitive = true
}

variable "ssh_known_host" {
  type = object({
    host      = string
    key_type  = string
    key_value = string
  })
}

variable "purge_on_delete" {
  type = bool
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "ssh ${var.name}"
    description  = "Clones its module over SSH."
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      terraform = {
        repository_url    = var.repository_url
        terraform_version = var.terraform_version
        ssh_private_key = {
          secret_value   = var.ssh_private_key
          secret_version = nonsensitive(sha256(var.ssh_private_key))
        }
        ssh_known_host = var.ssh_known_host
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
    display_name                          = "ssh ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {}
  }
  wait_for_completion = true
  purge_on_delete     = var.purge_on_delete
  timeouts = {
    create = "10m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
