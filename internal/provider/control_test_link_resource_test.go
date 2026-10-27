package provider

import (
	"errors"
	"fmt"
	"regexp"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func controlTestLinkConfig(endpoint string, s *recordServer, controlKey string) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}

resource "deixic_control_test_link" "branch_protection" {
  control_key   = %q
  test_id       = "dx.test.repository.default_branch_protection/v1"
  connection_id = "conn_github"
  name          = "Default branch protection (classic) on every repository"
}
`, endpoint, s.token, s.organizationID, s.workspaceID, controlKey)
}

func controlTestFixture(t *testing.T) (*recordServer, string) {
	t.Helper()
	testPrograms = newProgramFixture()
	testRepositories = map[string]*publicv1.BusinessObject{}
	fixture := newRecordServer(t)
	fixture.addType("type-test", "control_test")
	fixture.addType("type-repo", "repository")
	fixture.addType("type-control", "control")
	fixture.addObject("type-control", textField("key", "DX-CHG-03"), textField("name", "Change approval"))
	return fixture, "business_object_001"
}

func TestAccControlTestLinkCreatesTestAndLinksControl(t *testing.T) {
	fixture, controlID := controlTestFixture(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		CheckDestroy: func(_ *terraform.State) error {
			if len(testRepositories) != 0 {
				return errors.New("control test record was not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: controlTestLinkConfig(server.URL, fixture, "DX-CHG-03"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("deixic_control_test_link.branch_protection", "control_id", controlID),
				resource.TestCheckResourceAttr("deixic_control_test_link.branch_protection", "aggregation", "all"),
				func(*terraform.State) error {
					for id, object := range testRepositories {
						if fieldText(object, "key") != "github:conn_github:dx.test.repository.default_branch_protection/v1" {
							return fmt.Errorf("unexpected test key %q", fieldText(object, "key"))
						}
						if !testPrograms.links[id][controlID] {
							return errors.New("test is not linked to the control")
						}
					}
					return nil
				},
			),
		}},
	})
}

func TestAccControlTestLinkRejectsUnknownControl(t *testing.T) {
	fixture, _ := controlTestFixture(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config:      controlTestLinkConfig(server.URL, fixture, "DX-NOPE-01"),
			ExpectError: regexp.MustCompile(`no control has key "DX-NOPE-01"`),
		}},
	})
}
