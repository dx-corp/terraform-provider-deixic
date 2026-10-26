package provider

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
)

var testRepositories = map[string]*publicv1.BusinessObject{}
var testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}

func resetTestRepositories() {
	testRepositories = map[string]*publicv1.BusinessObject{}
	testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}
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
		if replay := s.creationReplays[request.GetIdempotencyKey()]; replay != nil {
			response := &publicv1.CreateBusinessObjectResponse{}
			if err := proto.Unmarshal(replay, response); err != nil {
				s.t.Error(err)
				return nil, &recordFailure{http.StatusInternalServerError, "internal", "invalid fixture replay"}
			}
			return response, nil
		}
		if request.GetTypeId() == "type-repo" && (request.GetSchemaRevision() != 2 || fieldText(&publicv1.BusinessObject{Values: request.GetValues()}, "title") == "") {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "published revision and display field are required"}
		}
		s.nextID++
		object := &publicv1.BusinessObject{ObjectId: fmt.Sprintf("repo_%d", s.nextID), TypeId: request.GetTypeId(), SchemaRevision: request.GetSchemaRevision(), Revision: 1, Values: request.GetValues()}
		testRepositories[object.GetObjectId()] = object
		response := &publicv1.CreateBusinessObjectResponse{Object: cloneRepository(object)}
		replay, err := proto.Marshal(response)
		if err != nil {
			s.t.Error(err)
			return nil, &recordFailure{http.StatusInternalServerError, "internal", "fixture encoding failed"}
		}
		s.creationReplays[request.GetIdempotencyKey()] = replay
		return response, nil
	}
	recordHandlers["BindBusinessObjectSource"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.BindBusinessObjectSourceRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		if replay := s.bindingReplays[request.GetIdempotencyKey()]; replay != nil {
			if s.bindingCommands[request.GetIdempotencyKey()] != string(body) {
				return nil, &recordFailure{http.StatusConflict, "aborted", "idempotency payload changed"}
			}
			response := &publicv1.BindBusinessObjectSourceResponse{}
			if err := proto.Unmarshal(replay, response); err != nil {
				s.t.Error(err)
				return nil, &recordFailure{http.StatusInternalServerError, "internal", "invalid fixture replay"}
			}
			return response, nil
		}
		object := testRepositories[request.GetObjectId()]
		if object != nil && testRepositoryBindings[object.GetObjectId()] != nil {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "Source binding already exists"}
		}
		if object == nil || object.GetRevision() != request.GetExpectedRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "revision changed"}
		}
		if len(request.GetFields()) > 64 {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "owner source-field limit exceeded"}
		}
		mappings := map[string]publicv1.BusinessSourceFieldProperty{}
		for _, mapping := range request.GetFields() {
			mappings[mapping.GetFieldId()] = mapping.GetProperty()
		}
		if testRepositoryBindings[object.GetObjectId()] != nil {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "Source binding already exists"}
		}
		validProperty := func(property publicv1.BusinessSourceFieldProperty) bool {
			return property == publicv1.BusinessSourceFieldProperty_BUSINESS_SOURCE_FIELD_PROPERTY_DISPLAY_NAME || property == publicv1.BusinessSourceFieldProperty_BUSINESS_SOURCE_FIELD_PROPERTY_EXTERNAL_ID
		}
		if len(request.GetGroupIds()) != 1 || request.GetGroupIds()[0] != "profile" || len(request.GetFields()) != 2 || !validProperty(mappings["title"]) || !validProperty(mappings["locator"]) {
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "source groups and complete supported field mappings are required"}
		}

		testRepositoryBindings[object.GetObjectId()] = request
		object.Revision++
		response := &publicv1.BindBusinessObjectSourceResponse{Object: cloneRepository(object)}
		replay, err := proto.Marshal(response)
		if err != nil {
			s.t.Error(err)
			return nil, &recordFailure{http.StatusInternalServerError, "internal", "fixture encoding failed"}
		}
		s.bindingCommands[request.GetIdempotencyKey()] = string(body)
		s.bindingReplays[request.GetIdempotencyKey()] = replay
		return response, nil
	}
	recordHandlers["GetBusinessObject"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.GetBusinessObjectRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		object := fixtureSourceObject(request.GetObjectId())
		if object == nil {
			return nil, &recordFailure{http.StatusNotFound, "not_found", "not found"}
		}
		if s.foreignBindOnRead && object.GetTypeId() == "type-repo" && testRepositoryBindings[object.GetObjectId()] == nil {
			testRepositoryBindings[object.GetObjectId()] = &publicv1.BindBusinessObjectSourceRequest{ConnectionId: "foreign"}
			object.Revision++
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
  repository_type_id = "type-repo"
  source_group_ids = ["profile"]
  source_fields = { title = "display_name", locator = "external_id" }
}
`, endpoint, s.token, s.organizationID, s.workspaceID, repository, repoID)
}

func TestAccProgramRepositoryBindingBindsAndLinks(t *testing.T) {
	testPrograms = newProgramFixture()
	testPrograms.links["program_1"] = map[string]bool{}
	testPrograms.programs["program_1"] = &publicv1.Program{ProgramId: "program_1", Revision: 1, PlanningStatus: "active"}
	testRepositories = map[string]*publicv1.BusinessObject{}
	testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}
	fixture := newRecordServer(t)
	addBindableRepositoryType(fixture)
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
						binding := testRepositoryBindings["repo_1"]
						if testRepositoryBindings[object.GetObjectId()] == nil || binding.GetRecordId() != "1211645548classic_protection" || binding.GetConnectionId() != "conn_github" || binding.GetFamilyStableId() != "resource_family:github.classic_protection.observed" {
							return fmt.Errorf("source binding intent wrong: %v", binding)
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

func addBindableRepositoryType(fixture *recordServer) {
	fixture.types = append(fixture.types, &publicv1.BusinessObjectType{TypeId: "type-repo", Revision: 2, DisplayFieldId: "title", Fields: []*publicv1.BusinessObjectTypeField{
		{FieldId: "title", GroupId: "profile", SourceMappable: true},
		{FieldId: "locator", GroupId: "profile", SourceMappable: true},
	}})
}

func TestAccRepositoryBindingRejectsInvalidSourceIntentBeforeCreate(t *testing.T) {
	for _, test := range []struct {
		name, old, replacement, diagnostic string
		nonText                            bool
	}{
		{name: "unknown group", old: `["profile"]`, replacement: `["missing"]`, diagnostic: "unknown source group"},
		{name: "unmapped field", old: `, locator = "external_id"`, replacement: "", diagnostic: "source group.*requires.*locator"},
		{name: "unknown property", old: `locator = "external_id"`, replacement: `locator = "caller_value"`, diagnostic: "unsupported source property"},
		{name: "non-text group", diagnostic: "cannot be source-mapped", nonText: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPrograms = newProgramFixture()
			testRepositories = map[string]*publicv1.BusinessObject{}
			fixture := newRecordServer(t)
			addBindableRepositoryType(fixture)
			if test.nonText {
				fixture.types[0].Fields[1].SourceMappable = false
			}
			server := fixture.start(t)
			config := repositoryBindingConfig(server.URL, fixture, "org/repo", "42")
			if test.old != "" {
				config = strings.Replace(config, test.old, test.replacement, 1)
			}
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testProviderFactories(server.Client()), Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(test.diagnostic)}}})
			if fixture.calls["CreateBusinessObject"] != 0 || fixture.calls["BindBusinessObjectSource"] != 0 {
				t.Fatalf("invalid intent caused writes: %v", fixture.calls)
			}
		})
	}
}

func repositoryBindingFixture(t *testing.T) (*recordServer, string) {
	t.Helper()
	testPrograms = newProgramFixture()
	testPrograms.links["program_1"] = map[string]bool{}
	testPrograms.programs["program_1"] = &publicv1.Program{ProgramId: "program_1", Revision: 1, PlanningStatus: "active"}
	testRepositories = map[string]*publicv1.BusinessObject{}
	testRepositoryBindings = map[string]*publicv1.BindBusinessObjectSourceRequest{}
	fixture := newRecordServer(t)
	addBindableRepositoryType(fixture)
	server := fixture.start(t)
	return fixture, server.URL
}

func TestAccRepositoryBindingChangedIntentCreatesAndBindsANewRecord(t *testing.T) {
	fixture, url := repositoryBindingFixture(t)
	config := repositoryBindingConfig(url, fixture, "org/repo", "42")
	changed := strings.Replace(config, `title = "display_name", locator = "external_id"`, `title = "external_id", locator = "display_name"`, 1)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testProviderFactories(nil), Steps: []resource.TestStep{
		{Config: config, Check: resource.TestCheckResourceAttr("deixic_program_repository_binding.repo", "id", "repo_1")},
		{Config: changed, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("deixic_program_repository_binding.repo", "id", "repo_2"), func(*terraform.State) error {
			if len(testRepositories) != 2 || fixture.calls["BindBusinessObjectSource"] != 2 {
				return fmt.Errorf("changed intent reused a previous source binding: %v", fixture.calls)
			}
			for _, field := range testRepositoryBindings["repo_2"].GetFields() {
				if field.GetFieldId() == "title" && field.GetProperty() != publicv1.BusinessSourceFieldProperty_BUSINESS_SOURCE_FIELD_PROPERTY_EXTERNAL_ID {
					return errors.New("changed source intent was not applied")
				}
			}
			return nil
		})},
	}})
}

func TestAccRepositoryBindingRecoversAfterBindingSucceedsAndLinkFails(t *testing.T) {
	fixture, url := repositoryBindingFixture(t)
	config := repositoryBindingConfig(url, fixture, "org/repo", "42")
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testProviderFactories(nil), Steps: []resource.TestStep{
		{PreConfig: func() { fixture.denyRelationshipWrites = true }, Config: config, ExpectError: regexp.MustCompile("Link repository to program")},
		{PreConfig: func() { fixture.denyRelationshipWrites = false }, Config: config, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("deixic_program_repository_binding.repo", "id", "repo_1"), func(*terraform.State) error {
			if len(testRepositories) != 1 || len(fixture.bindingReplays) != 1 || fixture.calls["BindBusinessObjectSource"] != 2 {
				return fmt.Errorf("accepted binding was repeated after a frozen create replay: %v", fixture.calls)
			}
			return nil
		})},
	}})
}

func TestAccRepositoryBindingRefusesForeignBindingWithoutMatchingReceipt(t *testing.T) {
	fixture, url := repositoryBindingFixture(t)
	fixture.foreignBindOnRead = true
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testProviderFactories(nil), Steps: []resource.TestStep{{Config: repositoryBindingConfig(url, fixture, "org/repo", "42"), ExpectError: regexp.MustCompile(`Source binding already\s+exists`)}}})
	if len(fixture.bindingReplays) != 0 || len(testPrograms.links["program_1"]) != 0 {
		t.Fatal("foreign source binding was silently adopted")
	}
}

func TestAccRepositoryBindingRejects65CompleteMappingsBeforeCreate(t *testing.T) {
	fixture, endpoint := repositoryBindingFixture(t)
	definition := fixture.types[0]
	definition.Fields = []*publicv1.BusinessObjectTypeField{{FieldId: "title", GroupId: "profile", SourceMappable: true}}
	mappings := []string{`title = "display_name"`}
	for index := 1; index < 65; index++ {
		id := fmt.Sprintf("field_%02d", index)
		definition.Fields = append(definition.Fields, &publicv1.BusinessObjectTypeField{FieldId: id, GroupId: "profile", SourceMappable: true})
		mappings = append(mappings, id+` = "display_name"`)
	}
	config := strings.Replace(repositoryBindingConfig(endpoint, fixture, "org/repo", "42"),
		`title = "display_name", locator = "external_id"`, strings.Join(mappings, ", "), 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(nil),
		Steps:                    []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`(?s)source_fields.*at most 64`)}},
	})
	if fixture.calls["ListBusinessObjectTypes"] != 0 || fixture.calls["CreateBusinessObject"] != 0 || fixture.calls["BindBusinessObjectSource"] != 0 || len(testRepositories) != 0 {
		t.Fatalf("oversized intent reached owner calls or created records: calls=%v records=%d", fixture.calls, len(testRepositories))
	}
}
