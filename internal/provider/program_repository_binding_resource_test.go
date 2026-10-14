package provider

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	consolev1 "github.com/dx-corp/terraform-provider-deixic/internal/consoleproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
)

var testRepositories = map[string]*consolev1.BusinessObject{}

func init() {
	consoleHandlers["CreateBusinessObject"] = func(s *consoleServer, body []byte) (proto.Message, *consoleFailure) {
		request := &consolev1.CreateBusinessObjectRequest{}
		if failure := decodeConsole(body, request); failure != nil {
			return nil, failure
		}
		for _, existing := range testRepositories {
			if fieldText(existing, "name") == fieldText(&consolev1.BusinessObject{Values: request.GetValues()}, "name") {
				return &consolev1.CreateBusinessObjectResponse{Object: existing}, nil
			}
		}
		s.nextID++
		object := &consolev1.BusinessObject{ObjectId: fmt.Sprintf("repo_%d", s.nextID), TypeId: request.GetTypeId(), Revision: 1, Values: request.GetValues()}
		testRepositories[object.GetObjectId()] = object
		return &consolev1.CreateBusinessObjectResponse{Object: object}, nil
	}
	consoleHandlers["BindBusinessObjectSource"] = func(_ *consoleServer, body []byte) (proto.Message, *consoleFailure) {
		request := &consolev1.BindBusinessObjectSourceRequest{}
		if failure := decodeConsole(body, request); failure != nil {
			return nil, failure
		}
		object := testRepositories[request.GetObjectId()]
		if object == nil || object.GetRevision() != request.GetExpectedRevision() {
			return nil, &consoleFailure{http.StatusConflict, "aborted", "revision changed"}
		}
		object.Source = &consolev1.BusinessSourceBinding{ConnectionId: request.GetConnectionId(), FamilyStableId: request.GetFamilyStableId(), RecordId: request.GetRecordId()}
		object.Revision++
		return &consolev1.BindBusinessObjectSourceResponse{Object: object}, nil
	}
	consoleHandlers["GetBusinessObject"] = func(_ *consoleServer, body []byte) (proto.Message, *consoleFailure) {
		request := &consolev1.GetBusinessObjectRequest{}
		if failure := decodeConsole(body, request); failure != nil {
			return nil, failure
		}
		object := testRepositories[request.GetObjectId()]
		if object == nil {
			return nil, &consoleFailure{http.StatusNotFound, "not_found", "not found"}
		}
		return &consolev1.GetBusinessObjectResponse{Object: object}, nil
	}
}

func repositoryBindingConfig(endpoint string, s *consoleServer, repository, repoID string) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}

resource "deixic_program_repository_binding" "repo" {
  program_id     = "program_1"
  connection_id  = "conn_github"
  repository     = %q
  github_repo_id = %q
}
`, endpoint, s.token, s.organizationID, s.workspaceID, repository, repoID)
}

func TestAccProgramRepositoryBindingBindsAndLinks(t *testing.T) {
	testPrograms = newProgramFixture()
	testPrograms.links["program_1"] = map[string]bool{}
	testRepositories = map[string]*consolev1.BusinessObject{}
	fixture := newConsoleServer(t)
	fixture.addType("type-repo", "repository")
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		CheckDestroy: func(_ *terraform.State) error {
			if len(testPrograms.links["program_1"]) != 0 {
				return errors.New("repository is still linked to the program")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: repositoryBindingConfig(server.URL, fixture, "dx-corp/mono", "1211645548"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("deixic_program_repository_binding.repo", "repository_type_id", "type-repo"),
					resource.TestCheckResourceAttr("deixic_program_repository_binding.repo", "family_stable_id", defaultRepositoryFamily),
					func(*terraform.State) error {
						object := testRepositories["repo_1"]
						if object.GetSource().GetRecordId() != "1211645548classic_protection" || object.GetSource().GetConnectionId() != "conn_github" {
							return fmt.Errorf("source binding wrong: %v", object.GetSource())
						}
						if !testPrograms.links["program_1"]["repo_1"] {
							return errors.New("program link missing")
						}
						return nil
					},
				),
			},
			{
				Config: repositoryBindingConfig(server.URL, fixture, "dx-corp/mono", "1211645548"),
				PreConfig: func() {
					delete(testPrograms.links["program_1"], "repo_1")
				},
				ExpectNonEmptyPlan: true,
				PlanOnly:           true,
			},
		},
	})
}
