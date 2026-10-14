resource "deixic_connection" "gcp" {
  provider_id     = "gcp"
  connection_id   = "conn_gcp_c9a63e57d7c7a81bf2a898c7851bcebe"
  display_name    = "Google Cloud: evalops-dev"
  profile         = "gcp_workload_identity"
  registration_id = "gcp-dx-corp-evalops-dev"
  actor_id        = "user_cae96cb237bc161dd754903c"
  account_id      = "evalops-prod"
  scopes          = ["https://www.googleapis.com/auth/cloud-platform"]
  gcp_identity = {
    service_account_email      = "dx-402d591f-c9a63e57-reader@evalops-prod.iam.gserviceaccount.com"
    scope_type                 = "projects"
    scope_id                   = "evalops-dev"
    workload_identity_provider = "projects/958713332840/locations/global/workloadIdentityPools/deixic-gcp-connections/providers/gke-platform-api"
    duration_seconds           = 3600
  }
}
