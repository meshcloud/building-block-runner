variable "var1" {
  type = any
}

variable "var2" {
  type = string
}

output "some_output" {
  value = var.var1
}
