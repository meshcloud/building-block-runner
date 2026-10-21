terraform {
  backend "http" {
    address        = "https://meshstack.example.com/api/terraform/state/workspace/test-workspace/buildingBlock/test-bb-id"
    lock_address   = "https://meshstack.example.com/api/terraform/state/workspace/test-workspace/buildingBlock/test-bb-id/lock"
    lock_method    = "POST"
    unlock_address = "https://meshstack.example.com/api/terraform/state/workspace/test-workspace/buildingBlock/test-bb-id/lock"
    unlock_method  = "DELETE"
  }
}
