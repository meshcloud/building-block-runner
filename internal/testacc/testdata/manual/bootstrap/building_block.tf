resource "meshstack_building_block_definition" "this" {
  metadata = {
    owned_by_workspace = meshstack_workspace.this.metadata.name
  }
  spec = {
    display_name = "manual ${var.name}"
    description  = "A manual building block, whose run hands its inputs back as outputs."
  }
  version_spec = {
    draft      = true
    runner_ref = meshstack_building_block_runner.this.ref
    implementation = {
      manual = {}
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
    }
  }
}

resource "meshstack_building_block" "this" {
  spec = {
    building_block_definition_version_ref = meshstack_building_block_definition.this.version_latest
    display_name                          = "manual ${var.name}"
    target_ref = {
      kind = "meshWorkspace"
      name = meshstack_workspace.this.metadata.name
    }
    inputs = {
      text = { value = jsonencode("hello") }
      num  = { value = jsonencode(42) }
      flag = { value = jsonencode(true) }
    }
  }
  wait_for_completion = true
  timeouts = {
    create = "5m"
    delete = "5m"
  }
}

output "building_block_uuid" {
  value = meshstack_building_block.this.metadata.uuid
}
