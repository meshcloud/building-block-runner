resource "terraform_data" "github" {
  count            = var.pipeline == "github" ? 1 : 0
  triggers_replace = local.run._links.self.href

  provisioner "local-exec" {
    command = "curl -fsS -H \"Authorization: Bearer $TOKEN\" -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' -d \"$BODY\" \"$URL\""
    environment = {
      URL   = var.trigger_url
      TOKEN = var.trigger_token
      BODY = sensitive(jsonencode({
        ref = "main"
        inputs = {
          buildingBlockRunUrl = local.run._links.self.href
          MESHSTACK_RUN_TOKEN = local.run.spec.runToken
        }
      }))
    }
  }
}
