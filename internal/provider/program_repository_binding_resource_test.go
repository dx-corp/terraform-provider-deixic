package provider

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
)

var (
	testRepositories = map[string]*publicv1.BusinessObject{}
	// testRepositoryBindings holds source bindings the way the owner does: beside
	// the record, never in the public projection.
	testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}
	// testRepositoryReplies replays keyed mutations with their original response.
	testRepositoryReplies = map[string]proto.Message{}
)

func resetTestRepositories() {
	testRepositories = map[string]*publicv1.BusinessObject{}
	testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}
	testRepositoryReplies = map[string]proto.Message{}
}

func cloneRepository(object *publicv1.BusinessObject) *publicv1.BusinessObject {
	clone, _ := proto.Clone(object).(*publicv1.BusinessObject)
	return clone
}

func init() {
	recordHandlers["CreateBusinessObject"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.CreateBusinessObjectRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		if reply, found := testRepositoryReplies[request.GetIdempotencyKey()]; found {
			return proto.Clone(reply), nil
		}
		s.nextID++
		object := &publicv1.BusinessObject{ObjectId: fmt.Sprintf("repo_%d", s.nextID), TypeId: request.GetTypeId(), Revision: 1, Values: request.GetValues()}
		testRepositories[object.GetObjectId()] = object
		reply := &publicv1.CreateBusinessObjectResponse{Object: cloneRepository(object)}
		testRepositoryReplies[request.GetIdempotencyKey()] = reply
		return proto.Clone(reply), nil
	}
	recordHandlers["BindBusinessObjectSource"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.BindBusinessObjectSourceRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		if reply, found := testRepositoryReplies[request.GetIdempotencyKey()]; found {
			return proto.Clone(reply), nil
		}
		object := testRepositories[request.GetObjectId()]
		if object == nil || object.GetRevision() != request.GetExpectedRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "revision changed"}
		}
		if testRepositoryBindings[object.GetObjectId()] != nil {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "Source binding already exists"}
		}
		testRepositoryBindings[object.GetObjectId()] = request
		object.Revision++
		reply := &publicv1.BindBusinessObjectSourceResponse{Object: cloneRepository(object)}
		testRepositoryReplies[request.GetIdempotencyKey()] = reply
		return proto.Clone(reply), nil
	}
	recordHandlers["GetBusinessObject"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.GetBusinessObjectRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		object := testRepositories[request.GetObjectId()]
		if object == nil {
			return nil, &recordFailure{http.StatusNotFound, "not_found", "not found"}
		}
		return &publicv1.GetBusinessObjectResponse{Object: object}, nil
	}
}

func repositoryBindingConfig(endpoint string, s *recordServer, repository, repoID string) string {
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
	resetTestRepositories()
	fixture := newRecordServer(t)
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
						binding := testRepositoryBindings["repo_1"]
						if binding.GetRecordId() != "1211645548classic_protection" || binding.GetConnectionId() != "conn_github" || binding.GetFamilyStableId() != defaultRepositoryFamily {
							return fmt.Errorf("source binding wrong: %v", binding)
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
