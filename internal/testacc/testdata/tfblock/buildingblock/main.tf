output "text" {
  value = trimspace(file("${path.module}/text.txt"))
}
