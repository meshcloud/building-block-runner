terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.12"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
  backend "gcs" {
    prefix = "building-block-runner/infra/github"
    bucket = "meshcloud-tf-states"
  }
}

provider "github" {
  owner = "meshcloud"
}

variable "repository_name" {
  description = "GitHub repository name"
  type        = string
  default     = "building-block-runner"
}

variable "repository_owner" {
  description = "GitHub repository owner"
  type        = string
  default     = "meshcloud"
}

variable "main_required_status_checks" {
  description = "Status check contexts required for merging into main"
  type        = list(string)
  default = [
    "block-runner-core - check",
    "manual-block-runner - check",
    "github-block-runner - check",
    "gitlab-block-runner - check",
    "azure-devops-block-runner - check",

    "manual-block-runner - image (PR)",
    "github-block-runner - image (PR)",
    "gitlab-block-runner - image (PR)",
    "azure-devops-block-runner - image (PR)",

    "run-controller - test",
    "tf-block-runner - test",

    "run-controller - image (PR)",
    "tf-block-runner - image (PR)",
  ]
}

variable "release_tag_pattern" {
  description = "Tag ref pattern to protect"
  type        = string
  default     = "refs/tags/v*"
}

variable "slack_webhook_url" {
  description = "Slack incoming webhook URL for GitHub Actions notifications"
  type        = string
  sensitive   = true
}

data "github_repository" "current" {
  full_name = "${var.repository_owner}/${var.repository_name}"
}

resource "github_repository_ruleset" "main" {
  name        = "main-protection"
  repository  = data.github_repository.current.name
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }

  rules {
    non_fast_forward = true

    # No direct push to main, require PR with one approval.
    pull_request {
      required_approving_review_count   = 1
      allowed_merge_methods             = ["rebase"]
      required_review_thread_resolution = true
    }

    # Require CI jobs to pass before merge.
    required_status_checks {
      strict_required_status_checks_policy = true

      dynamic "required_check" {
        for_each = toset(var.main_required_status_checks)
        content {
          context = required_check.value
        }
      }
    }
  }
}

resource "github_repository_ruleset" "tags" {
  name        = "release-tag-protection"
  repository  = data.github_repository.current.name
  target      = "tag"
  enforcement = "active"

  conditions {
    ref_name {
      include = [var.release_tag_pattern]
      exclude = []
    }
  }

  # Allow maintainers/admins to manage protected tags.
  bypass_actors {
    actor_type  = "RepositoryRole"
    actor_id    = 2 # maintain
    bypass_mode = "always"
  }

  bypass_actors {
    actor_type  = "RepositoryRole"
    actor_id    = 5 # admin
    bypass_mode = "always"
  }

  rules {
    creation = true
    update   = false
    deletion = false
  }
}

resource "tls_private_key" "pipeline_deploy_key" {
  algorithm = "ED25519"
}

resource "github_repository_deploy_key" "pipeline_read_only" {
  repository = data.github_repository.current.name
  title      = "pipeline-read-only"
  key        = tls_private_key.pipeline_deploy_key.public_key_openssh
  read_only  = true
}

resource "github_actions_secret" "slack_webhook_url" {
  repository  = data.github_repository.current.name
  secret_name = "SLACK_WEBHOOK_URL"
  value       = var.slack_webhook_url
}

output "pipeline_deploy_key_private" {
  # stored in vault so it's consumed by concourse pipeline
  description = "Private SSH key for the read-only pipeline deploy key"
  value       = tls_private_key.pipeline_deploy_key.private_key_openssh
  sensitive   = true
}

resource "github_repository_autolink_reference" "clickup" {
  repository = data.github_repository.current.name

  key_prefix          = "CU-"
  target_url_template = "https://app.clickup.com/t/<num>"
  is_alphanumeric     = true
}

resource "github_repository_autolink_reference" "support_ticket" {
  repository = data.github_repository.current.name

  key_prefix          = "BD-"
  target_url_template = "https://support.meshcloud.io/agent/tickets/<num>"
  is_alphanumeric     = false
}
