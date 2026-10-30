terraform {
  required_providers {
    missing = {
      source = "meshcloud/does-not-exist"
    }
  }
}
