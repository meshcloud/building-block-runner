variable "sleep_seconds" {
  type = number
}

resource "terraform_data" "sleep" {
  input = plantimestamp()

  provisioner "local-exec" {
    command = "echo MESHSTACK_API_TOKEN is $${MESHSTACK_API_TOKEN:-unset}; sleep ${var.sleep_seconds}"
  }
}
