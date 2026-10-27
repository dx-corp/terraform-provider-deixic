variable "repository_program_id" {
  type        = string
  description = "Existing authorized program whose pinned schema declares repositories targeting repository_type_id."
}
variable "repository_type_id" {
  type        = string
  description = "Published repository type with a text display field and source-mappable selected groups."
}
variable "source_group_ids" {
  type = set(string)
}
variable "source_fields" {
  type        = map(string)
  description = "Every selected field ID mapped to display_name or external_id."
}

resource "deixic_program_repository_binding" "mono" {
  program_id         = var.repository_program_id
  connection_id      = "conn_oauth_..."
  repository         = "dx-corp/mono"
  github_repo_id     = "1211645548"
  repository_type_id = var.repository_type_id
  source_group_ids   = var.source_group_ids
  source_fields      = var.source_fields
}
