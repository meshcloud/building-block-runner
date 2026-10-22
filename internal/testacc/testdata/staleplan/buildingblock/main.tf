terraform {
  backend "local" {
    path          = "${var.state_dir}/terraform.tfstate"
    workspace_dir = "${var.state_dir}/ws"
  }
}

variable "state_dir" {
  type = string
}

resource "terraform_data" "this" {
  input = plantimestamp()
}

output "text" {
  value = "hello"
}
