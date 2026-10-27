package provider

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
)

// programFixture is the program owner: program records and their framework links.
type programFixture struct {
	programs      map[string]*publicv1.Program
	links         map[string]map[string]bool
	creates       int
	deleted       bool
	initialStatus string
}

var testPrograms = &programFixture{}

func newProgramFixture() *programFixture {
	return &programFixture{initialStatus: "planned", programs: map[string]*publicv1.Program{}, links: map[string]map[string]bool{}}
}

func init() {
	notFound := &recordFailure{http.StatusNotFound, "not_found", "Program not found"}
	recordHandlers["CreateProgram"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.CreateProgramRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		f := testPrograms
		f.creates++
		program := &publicv1.Program{ProgramId: fmt.Sprintf("program_%d", f.creates), Revision: 1, Name: request.GetName(), Goal: request.GetGoal(), OwnerLabel: request.GetOwnerLabel(), PlanningStatus: f.initialStatus}
		f.programs[program.GetProgramId()] = program
		f.links[program.GetProgramId()] = map[string]bool{}
		return &publicv1.CreateProgramResponse{Program: program}, nil
	}
	recordHandlers["GetProgram"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.GetProgramRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		program := testPrograms.programs[request.GetProgramId()]
		if program == nil {
			return nil, notFound
		}
		return &publicv1.GetProgramResponse{Program: program}, nil
	}
	recordHandlers["UpdateProgram"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.UpdateProgramRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		program := testPrograms.programs[request.GetProgramId()]
		if program == nil {
			return nil, notFound
		}
		if request.GetExpectedRevision() != program.GetRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "revision changed"}
		}
		switch request.GetPlanningStatus() {
		case "planned", "active", "paused", "archived":
		default:
			return nil, &recordFailure{http.StatusBadRequest, "invalid_argument", "Unsupported program planning status"}
		}
		program.Name, program.Goal, program.OwnerLabel = request.GetName(), request.GetGoal(), request.GetOwnerLabel()
		program.PlanningStatus = request.GetPlanningStatus()
		program.Revision++
		return &publicv1.UpdateProgramResponse{Program: program}, nil
	}
	recordHandlers["DeleteBusinessObject"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.DeleteBusinessObjectRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		if testPrograms.programs[request.GetObjectId()] == nil && testRepositories[request.GetObjectId()] == nil {
			return nil, notFound
		}
		if source := fixtureSourceObject(request.GetObjectId()); source == nil || source.GetRevision() != request.GetExpectedRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "delete source revision changed"}
		}
		delete(testPrograms.programs, request.GetObjectId())
		delete(testRepositories, request.GetObjectId())
		testPrograms.deleted = true
		return &publicv1.DeleteBusinessObjectResponse{Object: &publicv1.BusinessObject{ObjectId: request.GetObjectId(), Deleted: true}}, nil
	}
	recordHandlers["CreateBusinessObjectRelationship"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.CreateBusinessObjectRelationshipRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		if s.denyRelationshipWrites {
			return nil, &recordFailure{http.StatusServiceUnavailable, "unavailable", "relationship owner unavailable"}
		}
		relationship := request.GetRelationship()
		source := fixtureSourceObject(relationship.GetSourceObjectId())
		if source == nil {
			return nil, notFound
		}
		if request.GetExpectedRevision() <= 0 || request.GetExpectedRevision() != source.GetRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "relationship source revision changed"}
		}
		advanceFixtureSource(relationship.GetSourceObjectId())

		if testPrograms.links[relationship.GetSourceObjectId()] == nil {
			testPrograms.links[relationship.GetSourceObjectId()] = map[string]bool{}
		}
		testPrograms.links[relationship.GetSourceObjectId()][relationship.GetTargetObjectId()] = true
		return &publicv1.CreateBusinessObjectRelationshipResponse{}, nil
	}
	recordHandlers["DeleteBusinessObjectRelationship"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.DeleteBusinessObjectRelationshipRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		relationship := request.GetRelationship()
		source := fixtureSourceObject(relationship.GetSourceObjectId())
		if source == nil {
			return nil, notFound
		}
		if request.GetExpectedRevision() <= 0 || request.GetExpectedRevision() != source.GetRevision() {
			return nil, &recordFailure{http.StatusConflict, "aborted", "relationship source revision changed"}
		}
		advanceFixtureSource(relationship.GetSourceObjectId())

		delete(testPrograms.links[relationship.GetSourceObjectId()], relationship.GetTargetObjectId())
		return &publicv1.DeleteBusinessObjectRelationshipResponse{}, nil
	}
	recordHandlers["ListBusinessObjectRelationships"] = func(_ *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.ListBusinessObjectRelationshipsRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		var targets []string
		for target := range testPrograms.links[request.GetObjectId()] {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		response := &publicv1.ListBusinessObjectRelationshipsResponse{}
		for _, target := range targets {
			response.Relationships = append(response.Relationships, &publicv1.BusinessObjectRelationship{
				SourceObjectId: request.GetObjectId(), RelationshipId: request.GetRelationshipId(), TargetObjectId: target,
			})
		}
		return response, nil
	}
}

func programConfig(endpoint string, s *recordServer, goal, frameworks string) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}

resource "deixic_program" "soc2" {
  name          = "dx-corp SOC 2"
  goal          = %q
  owner_label   = "Jonathan Haas"
  framework_ids = %s
}
`, endpoint, s.token, s.organizationID, s.workspaceID, goal, frameworks)
}

func TestAccProgramLifecycleWithFrameworks(t *testing.T) {
	testPrograms = newProgramFixture()
	fixture := newRecordServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		CheckDestroy: func(_ *terraform.State) error {
			if !testPrograms.deleted || len(testPrograms.programs) != 0 {
				return errors.New("program was not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: programConfig(server.URL, fixture, "SOC 2 Type 1", `["fw-a", "fw-b"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("deixic_program.soc2", "id", "program_1"),
					resource.TestCheckResourceAttr("deixic_program.soc2", "name", "dx-corp SOC 2"),
					resource.TestCheckResourceAttr("deixic_program.soc2", "framework_ids.#", "2"),
					resource.TestCheckResourceAttr("deixic_program.soc2", "revision", "3"),
				),
			},
			{
				Config: programConfig(server.URL, fixture, "SOC 2 Type 2", `["fw-b", "fw-c"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("deixic_program.soc2", "goal", "SOC 2 Type 2"),
					resource.TestCheckResourceAttr("deixic_program.soc2", "revision", "6"),
					resource.TestCheckTypeSetElemAttr("deixic_program.soc2", "framework_ids.*", "fw-c"),
					func(*terraform.State) error {
						if testPrograms.links["program_1"]["fw-a"] || !testPrograms.links["program_1"]["fw-c"] {
							return fmt.Errorf("links not reconciled: %v", testPrograms.links["program_1"])
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccProgramRefreshRemovesDeletedProgram(t *testing.T) {
	testPrograms = newProgramFixture()
	fixture := newRecordServer(t)
	server := fixture.start(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(server.Client()),
		Steps: []resource.TestStep{
			{Config: programConfig(server.URL, fixture, "g", `[]`)},
			{
				PreConfig:          func() { testPrograms.programs = map[string]*publicv1.Program{} },
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccProgramMetadataPreservesOwnerStatus(t *testing.T) {
	for _, status := range []string{"active", "paused"} {
		t.Run(status, func(t *testing.T) {
			testPrograms = newProgramFixture()
			testPrograms.initialStatus = status
			fixture := newRecordServer(t)
			server := fixture.start(t)
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testProviderFactories(server.Client()), Steps: []resource.TestStep{
				{Config: programConfig(server.URL, fixture, "First goal", `[]`)},
				{Config: programConfig(server.URL, fixture, "Updated goal", `[]`), Check: func(*terraform.State) error {
					if got := testPrograms.programs["program_1"].GetPlanningStatus(); got != status {
						return fmt.Errorf("metadata update changed %s status to %s", status, got)
					}
					return nil
				}},
			}})
		})
	}
}

func fixtureSourceObject(id string) *publicv1.BusinessObject {
	if program := testPrograms.programs[id]; program != nil {
		return &publicv1.BusinessObject{ObjectId: id, Revision: program.GetRevision()}
	}
	return testRepositories[id]
}
func advanceFixtureSource(id string) {
	if program := testPrograms.programs[id]; program != nil {
		program.Revision++
		return
	}
	testRepositories[id].Revision++
}
