# The GitLab provider manages trigger tokens only and cannot start a pipeline.
resource "terraform_data" "gitlab" {
  count            = var.pipeline == "gitlab" ? 1 : 0
  triggers_replace = local.run._links.self.href

  provisioner "local-exec" {
    command = "curl -fsS -F token=\"$TOKEN\" -F ref=main -F \"variables[MESHSTACK_RUN_URL]=$RUN_URL\" -F \"variables[MESHSTACK_RUN_TOKEN]=$RUN_TOKEN\" \"$URL\""
    environment = {
      URL       = var.trigger_url
      TOKEN     = var.trigger_token
      RUN_URL   = local.run._links.self.href
      RUN_TOKEN = sensitive(local.run.spec.runToken)
    }
  }
}
