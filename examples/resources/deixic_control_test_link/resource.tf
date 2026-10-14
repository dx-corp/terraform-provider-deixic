resource "deixic_control_test_link" "branch_protection" {
  control_key   = "DX-CHG-03"
  test_id       = "dx.test.repository.default_branch_protection/v1"
  connection_id = "conn_oauth_..."
  name          = "Default branch protection (classic) on every repository"
}
