package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
)

// libraryFixture models the library owner: a plan over the selected packs whose
// APPLY answers applied=false for the first runningPolls calls, like a server job.
type libraryFixture struct {
	planDigest   string
	changes      int
	runningPolls int
	applyCalls   int
	applied      bool
	keys         map[string]bool
}

var testLibrary = &libraryFixture{}

func init() {
	recordHandlers["ListBusinessBlueprints"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		if failure := decodeRecord(body, &publicv1.ListBusinessBlueprintsRequest{}); failure != nil {
			return nil, failure
		}
		return &publicv1.ListBusinessBlueprintsResponse{Blueprints: []*publicv1.BusinessBlueprint{
			{BlueprintId: "cerebro-framework-soc2", Version: 1, ContentDigest: "old"},
			{BlueprintId: "cerebro-framework-soc2", Version: 2, ContentDigest: "new"},
			{BlueprintId: "cerebro-framework-iso27001", Version: 1, ContentDigest: "iso"},
		}}, nil
	}
	recordHandlers["ReconcileComplianceLibrary"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.ReconcileComplianceLibraryRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		library := testLibrary
		response := &publicv1.ReconcileComplianceLibraryResponse{LibraryCloneId: "clone-1", PlanDigest: library.planDigest}
		if request.GetMode() == publicv1.ComplianceLibraryReconcileMode_COMPLIANCE_LIBRARY_RECONCILE_MODE_PREVIEW {
			if !library.applied {
				for i := 0; i < library.changes; i++ {
					response.Changes = append(response.Changes, &publicv1.ComplianceLibraryChange{Kind: publicv1.ComplianceLibraryChangeKind_COMPLIANCE_LIBRARY_CHANGE_KIND_ADD})
				}
			}
			return response, nil
		}
		if request.GetIdempotencyKey() == "" || request.GetExpectedPlanDigest() != library.planDigest {
			return nil, &recordFailure{http.StatusConflict, "aborted", "stale plan digest"}
		}
		library.keys[request.GetIdempotencyKey()] = true
		library.applyCalls++
		if library.applyCalls > library.runningPolls {
			library.applied = true
			response.Applied = true
			for i := 0; i < library.changes; i++ {
				response.Changes = append(response.Changes, &publicv1.ComplianceLibraryChange{})
			}
		}
		return response, nil
	}
}

func libraryConfig(endpoint string, s *recordServer, ids string) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}

resource "deixic_compliance_library" "lib" {
  blueprint_ids = %s
}
`, endpoint, s.token, s.organizationID, s.workspaceID, ids)
}

func TestAccComplianceLibraryPollsServerJobAndRecordsPlanDigest(t *testing.T) {
	libraryPollInterval = time.Millisecond
	testLibrary = &libraryFixture{planDigest: "plan-abc", changes: 4204, runningPolls: 3, keys: map[string]bool{}}
	fixture := newRecordServer(t)
	fixture.addType("type-test", "control_test")
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config: libraryConfig(server.URL, fixture, `["cerebro-framework-soc2"]`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "id", "clone-1"),
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "plan_digest", "plan-abc"),
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "applied_changes", "4204"),
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "library_type_id", "type-test"),
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "selections.0.version", "2"),
				resource.TestCheckResourceAttr("deixic_compliance_library.lib", "selections.0.content_digest", "new"),
				func(*terraform.State) error {
					if testLibrary.applyCalls != 4 || len(testLibrary.keys) != 1 {
						return fmt.Errorf("want 4 apply polls on one idempotency key, got %d calls and %d keys", testLibrary.applyCalls, len(testLibrary.keys))
					}
					return nil
				},
			),
		}},
	})
}

func TestAccComplianceLibraryRejectsUnknownPack(t *testing.T) {
	testLibrary = &libraryFixture{planDigest: "p", keys: map[string]bool{}}
	fixture := newRecordServer(t)
	fixture.addType("type-test", "control_test")
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{{
			Config:      libraryConfig(server.URL, fixture, `["cerebro-framework-nope"]`),
			ExpectError: regexp.MustCompile(`not in the catalog`),
		}},
	})
}
