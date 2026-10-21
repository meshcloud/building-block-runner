variable "var3" {
  description = "Some variable with (default) type any"
}

variable "var4" {
  type = object({
    some = string
    flag = bool
  })

  validation {
    condition = true
    error_message = "Oh uh"
  }
}
