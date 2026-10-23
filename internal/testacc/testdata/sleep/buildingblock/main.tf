variable "sleep_seconds" {
  type = number
}

resource "terraform_data" "sleep" {
  input = plantimestamp()

  provisioner "local-exec" {
    command = "sleep ${var.sleep_seconds}"
  }
}
