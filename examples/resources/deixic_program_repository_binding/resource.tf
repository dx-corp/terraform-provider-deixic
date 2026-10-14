resource "deixic_program_repository_binding" "mono" {
  program_id     = deixic_program.soc2.id
  connection_id  = "conn_oauth_..."
  repository     = "dx-corp/mono"
  github_repo_id = "1211645548"
}
