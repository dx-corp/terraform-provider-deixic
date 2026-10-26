# SOC 2 library, repository bindings and the DX-CHG-03 branch-protection test
# for an existing program with a compatible pinned repository relationship.
#
#   export DEIXIC_TOKEN=<fresh bearer token>
#   ./apply.sh
#
# DEIXIC_TOKEN is the only secret. Everything else is a public identifier.

terraform {
  required_providers {
    deixic = {
      source = "dx-corp/deixic"
    }
  }
}

variable "endpoint" {
  type        = string
  default     = "https://app.deixic.com"
  description = "Base URL that serves the public RPCs (app.deixic.com routes /deixicpublic.v1.DeixicPublicService/ to the platform API)."
}

variable "github_connection_id" {
  type    = string
  default = "conn_oauth_ede3ac8e0d1bad9c414208a9e39046d2"
}

variable "repositories" {
  type = map(string)
  default = {
    "dx-corp/mono"   = "1211645548"
    "dx-corp/deixic" = "1268863528"
    "dx-corp/k8s"    = "1352875407"
  }
  description = "GitHub repository full name to numeric repository ID."
}

variable "repository_program_id" {
  type        = string
  description = "Existing authorized program whose pinned schema declares repositories targeting repository_type_id."
}
variable "repository_type_id" {
  type        = string
  description = "Published type with a text display field and source-mappable selected groups."
}
variable "source_group_ids" {
  type = set(string)
}
variable "source_fields" {
  type        = map(string)
  description = "Every selected field ID mapped to display_name or external_id."
}

provider "deixic" {
  endpoint        = var.endpoint
  organization_id = "org_402d591f3ab22ef377ddb83f921e687d"
  workspace_id    = "org_402d591f3ab22ef377ddb83f921e687d-default"
  # token comes from DEIXIC_TOKEN
}

resource "deixic_compliance_library" "soc2" {
  blueprint_ids = ["cerebro-framework-soc2"]
}

resource "deixic_program_repository_binding" "repo" {
  for_each = var.repositories

  program_id         = var.repository_program_id
  connection_id      = var.github_connection_id
  repository         = each.key
  github_repo_id     = each.value
  repository_type_id = var.repository_type_id
  source_group_ids   = var.source_group_ids
  source_fields      = var.source_fields
  depends_on         = [deixic_compliance_library.soc2]
}

resource "deixic_control_test_link" "dx_chg_03" {
  repository_type_id = var.repository_type_id
  control_key        = "DX-CHG-03"
  test_id            = "dx.test.repository.default_branch_protection/v1"
  connection_id      = var.github_connection_id
  name               = "Default branch protection (classic) on every repository"
  depends_on         = [deixic_compliance_library.soc2, deixic_program_repository_binding.repo]
}

output "program_id" {
  value = var.repository_program_id
}

output "library_plan_digest" {
  value = deixic_compliance_library.soc2.plan_digest
}
