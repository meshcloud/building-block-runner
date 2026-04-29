terraform {
  backend "http" {
    address = "https://meshstack.example.com/api/terraform/state/workspace/test-workspace/buildingBlock/test-bb-id"
    headers = {
      Authorization = "Bearer ephemeral-run-token"
    }
  }
}
