package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func providerBlock(endpoint string, s *consoleServer) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}
`, endpoint, s.token, s.organizationID, s.workspaceID)
}

func TestAccBusinessBlueprintsDataSourceFiltersByPrefix(t *testing.T) {
	fixture := newConsoleServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config: providerBlock(server.URL, fixture) + `
data "deixic_business_blueprints" "soc2" { id_prefix = "cerebro-framework-soc2" }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.deixic_business_blueprints.soc2", "blueprints.#", "2"),
				resource.TestCheckResourceAttr("data.deixic_business_blueprints.soc2", "blueprints.1.content_digest", "new"),
			),
		}},
	})
}

func TestAccFrameworksAndControlsDataSources(t *testing.T) {
	fixture := newConsoleServer(t)
	fixture.addType("type-framework", "framework")
	fixture.addType("type-control", "control")
	fixture.addObject("type-framework", textField("key", "soc2"), textField("name", "SOC 2"))
	fixture.addObject("type-framework", textField("key", "iso27001"), textField("name", "ISO 27001"))
	fixture.addObject("type-control", textField("key", "DX-CHG-03"), textField("name", "Change approval"))
	fixture.addObject("type-control", textField("key", "DX-CHG-04"), textField("name", "Change testing"))
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config: providerBlock(server.URL, fixture) + `
data "deixic_frameworks" "soc" { search = "soc" }
data "deixic_controls" "chg03" { key = "DX-CHG-03" }
data "deixic_controls" "all" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.deixic_frameworks.soc", "records.#", "1"),
				resource.TestCheckResourceAttr("data.deixic_frameworks.soc", "records.0.key", "soc2"),
				resource.TestCheckResourceAttr("data.deixic_controls.chg03", "records.#", "1"),
				resource.TestCheckResourceAttr("data.deixic_controls.chg03", "records.0.name", "Change approval"),
				resource.TestCheckResourceAttr("data.deixic_controls.all", "records.#", "2"),
			),
		}},
	})
}

func TestAccRecordsDataSourceReportsMissingAndAmbiguousTypes(t *testing.T) {
	fixture := newConsoleServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config:      providerBlock(server.URL, fixture) + `data "deixic_controls" "x" {}`,
			ExpectError: regexpMust(`no "control" type is installed`),
		}},
	})
	fixture.addType("type-a", "control")
	fixture.addType("type-b", "control")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config:      providerBlock(server.URL, fixture) + `data "deixic_controls" "x" {}`,
			ExpectError: regexpMust(`2 types provide "control"`),
		}},
	})
}

func regexpMust(pattern string) *regexp.Regexp { return regexp.MustCompile(pattern) }
