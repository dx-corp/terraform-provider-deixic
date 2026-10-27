package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// connectorsServer answers the managed exchange registration.
func connectorsServer(t *testing.T, status int, seen *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != managedExchangePath || request.Header.Get("Authorization") != "Bearer test-token" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		body := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		*seen = body
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = writer.Write([]byte(`{"code":"managed_exchange_failed","message":"Managed exchange failed"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"connectionId":"conn_gcp_1","credentialRefs":{"access_token":"ref://gcp/1"},"expiresAtUnix":1900000000}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func connectionConfig(endpoint, connectors string, s *recordServer) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint            = %q
  connectors_endpoint = %q
  token               = %q
  organization_id     = %q
  workspace_id        = %q
}

resource "deixic_connection" "gcp" {
  provider_id     = "gcp"
  connection_id   = "conn_gcp_1"
  display_name    = "Google Cloud: evalops-dev"
  profile         = "gcp_workload_identity"
  registration_id = "gcp-dx-corp-evalops-dev"
  actor_id        = "user_1"
  account_id      = "evalops-prod"
  scopes          = ["https://www.googleapis.com/auth/cloud-platform"]
  gcp_identity = {
    service_account_email      = "reader@evalops-prod.iam.gserviceaccount.com"
    scope_type                 = "projects"
    scope_id                   = "evalops-dev"
    workload_identity_provider = "projects/1/locations/global/workloadIdentityPools/p/providers/q"
    duration_seconds           = 3600
  }
}
`, endpoint, connectors, s.token, s.organizationID, s.workspaceID)
}

func TestAccConnectionEnrollsByReference(t *testing.T) {
	var seen map[string]any
	connectors := connectorsServer(t, http.StatusOK, &seen)
	fixture := newRecordServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config: connectionConfig(server.URL, connectors.URL, fixture),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("deixic_connection.gcp", "id", "conn_gcp_1"),
				resource.TestCheckResourceAttr("deixic_connection.gcp", "issued_credential_refs.access_token", "ref://gcp/1"),
				resource.TestCheckResourceAttr("deixic_connection.gcp", "service_principal_id", "connectors-oauth-exchange"),
				func(_ *terraform.State) error {
					binding, _ := seen["exchange"].(map[string]any)["binding"].(map[string]any)
					if binding["organizationId"] != fixture.organizationID || binding["actorId"] != "user_1" {
						return fmt.Errorf("unexpected binding %v", binding)
					}
					return nil
				},
			),
		}},
	})
}

func TestAccConnectionSurfacesExchangeFailure(t *testing.T) {
	var seen map[string]any
	connectors := connectorsServer(t, http.StatusBadGateway, &seen)
	fixture := newRecordServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config:      connectionConfig(server.URL, connectors.URL, fixture),
			ExpectError: regexp.MustCompile(`managed_exchange_failed`),
		}},
	})
}
