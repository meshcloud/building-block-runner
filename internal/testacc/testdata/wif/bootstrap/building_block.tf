variable "terraform_version" {
  type = string
}

variable "repository_url" {
  type = string
}

variable "purge_on_delete" {
  type = bool
}

resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "wif ${var.name}"
    description  = "Outputs the workload identity token of its run."
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
    outputs = {
      wif_token = {
        display_name    = "Workload identity token"
        type            = "STRING"
        assignment_type = "NONE"
      }
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "wif ${var.name}"
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

output "definition_uuid" {
  value = meshstack_building_block_definition.this.metadata.uuid
}
