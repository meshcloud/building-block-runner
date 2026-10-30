resource "terraform_data" "azuredevops" {
  count            = var.pipeline == "azuredevops" ? 1 : 0
  triggers_replace = local.run._links.self.href

  provisioner "local-exec" {
    command = "curl -fsS -u \":$PAT\" -H 'Content-Type: application/json' -d \"$BODY\" \"$URL\""
    environment = {
      URL = var.trigger_url
      PAT = var.trigger_token
      BODY = sensitive(jsonencode({
        resources = { repositories = { self = { refName = "refs/heads/main" } } }
        templateParameters = {
          MESHSTACK_RUN_URL   = local.run._links.self.href
          MESHSTACK_RUN_TOKEN = local.run.spec.runToken
        }
      }))
    }
  }
}
