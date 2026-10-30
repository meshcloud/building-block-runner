# A local backend keeps tofu off the proxy, so that a toxic hits only the runner's requests.
terraform {
  backend "local" {}
}

variable "gate_dir" {
  type = string
}

resource "terraform_data" "gate" {
  provisioner "local-exec" {
    command = "touch '${var.gate_dir}/waiting'; while [ ! -e '${var.gate_dir}/released' ]; do sleep 1; done"
  }
}

output "text" {
  value = "hello"
}
