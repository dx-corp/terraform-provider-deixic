package provider

import (
	"context"
	"fmt"
	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrameworkRelationshipsFenceEachMutationAndReconcileLostResponse(t *testing.T) {
	revision := int64(1)
	linked := map[string]bool{}
	keys := map[string]string{}
	mutations := 0
	loseNext := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var output proto.Message
		switch r.URL.Path {
		case "/deixicpublic.v1.DeixicPublicService/GetBusinessObject":
			output = &publicv1.GetBusinessObjectResponse{Object: &publicv1.BusinessObject{ObjectId: "program", Revision: revision}}
		case "/deixicpublic.v1.DeixicPublicService/ListBusinessObjectRelationships":
			response := &publicv1.ListBusinessObjectRelationshipsResponse{}
			for id := range linked {
				response.Relationships = append(response.Relationships, &publicv1.BusinessObjectRelationship{SourceObjectId: "program", RelationshipId: "frameworks", TargetObjectId: id})
			}
			output = response
		case "/deixicpublic.v1.DeixicPublicService/CreateBusinessObjectRelationship", "/deixicpublic.v1.DeixicPublicService/DeleteBusinessObjectRelationship":
			request := &publicv1.CreateBusinessObjectRelationshipRequest{}
			if err := proto.Unmarshal(body, request); err != nil {
				t.Error(err)
				return
			}
			fingerprint := fmt.Sprintf("%s:%d:%s", r.URL.Path, request.GetExpectedRevision(), request.GetRelationship().GetTargetObjectId())
			if previous, exists := keys[request.GetIdempotencyKey()]; exists && previous != fingerprint {
				t.Errorf("idempotency key reused with changed intent: %q", request.GetIdempotencyKey())
			}
			keys[request.GetIdempotencyKey()] = fingerprint
			if request.GetExpectedRevision() <= 0 || request.GetExpectedRevision() != revision {
				http.Error(w, "revision fence missing or stale", http.StatusConflict)
				return
			}
			if r.URL.Path == "/deixicpublic.v1.DeixicPublicService/CreateBusinessObjectRelationship" {
				linked[request.GetRelationship().GetTargetObjectId()] = true
			} else {
				delete(linked, request.GetRelationship().GetTargetObjectId())
			}
			revision++
			mutations++
			if loseNext {
				loseNext = false
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("test server cannot simulate a lost response")
					return
				}
				connection, _, err := hijacker.Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}
			output = &publicv1.CreateBusinessObjectRelationshipResponse{Object: &publicv1.BusinessObject{ObjectId: "program", Revision: revision}}
		default:
			t.Errorf("unexpected public route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		payload, err := proto.Marshal(output)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/proto")
		w.Header().Set("Connect-Protocol-Version", "1")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "org", "workspace", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, step := range []struct {
		id     string
		remove bool
	}{{"a", false}, {"b", false}, {"a", true}, {"a", true}, {"a", false}} {
		if err := client.relateFramework(ctx, "program", step.id, step.remove); err != nil {
			t.Fatal(err)
		}
	}
	if revision != 5 || mutations != 4 {
		t.Fatalf("revision=%d mutations=%d; want 5 and 4", revision, mutations)
	}
	loseNext = true
	if err := client.relateFramework(ctx, "program", "c", false); err == nil {
		t.Fatal("lost response must surface uncertainty")
	}
	if err := client.relateFramework(ctx, "program", "c", false); err != nil {
		t.Fatal(err)
	}
	if revision != 6 || mutations != 5 {
		t.Fatalf("accepted loss was repeated: revision=%d mutations=%d", revision, mutations)
	}
}

func TestRelationshipRemovalRefusesHiddenPeerInsteadOfClaimingAbsence(t *testing.T) {
	edgeExists := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/deixicpublic.v1.DeixicPublicService/ListBusinessObjectRelationships" {
			if r.URL.Path == "/deixicpublic.v1.DeixicPublicService/GetBusinessObject" {
				payload, _ := proto.Marshal(&publicv1.GetBusinessObjectResponse{Object: &publicv1.BusinessObject{ObjectId: "program", Revision: 1}})
				w.Header().Set("Content-Type", "application/proto")
				w.Header().Set("Connect-Protocol-Version", "1")
				_, _ = w.Write(payload)
				return
			}
			edgeExists = false
			t.Errorf("unexpected mutation after peer denial: %s", r.URL.Path)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		request := &publicv1.ListBusinessObjectRelationshipsRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Connect-Protocol-Version", "1")
		if request.GetPeerObjectId() == "hidden" && (request.GetRelationshipId() == "frameworks" || request.GetRelationshipId() == "repositories" || request.GetRelationshipId() == "controls") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"permission_denied","message":"selected peer is not visible"}`))
			return
		}
		// Audience-filtered discovery omits the edge while it remains in owner state.
		w.Header().Set("Content-Type", "application/proto")
		payload, err := proto.Marshal(&publicv1.ListBusinessObjectRelationshipsResponse{})
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "org", "workspace", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.relateFramework(context.Background(), "program", "", true); err == nil {
		t.Fatal("missing peer must not become a broad absence query")
	}
	if err := client.relateFramework(context.Background(), "program", "hidden", true); err == nil {
		t.Fatal("hidden peer cannot establish absence or a completed removal")
	}

	implementation := &programRepositoryBindingResource{client: client}
	schema := resource.SchemaResponse{}
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := programRepositoryBindingModel{ID: types.StringValue("hidden"), ProgramID: types.StringValue("program"), ConnectionID: types.StringValue("connection"), Repository: types.StringValue("org/repo"), GitHubRepoID: types.StringValue("42"), FamilyStableID: types.StringValue("family"), RecordSuffix: types.StringValue("record"), RepositoryTypeID: types.StringValue("type"), SourceGroupIDs: types.SetNull(types.StringType), SourceFields: types.MapNull(types.StringType)}
	if diagnostics := state.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	response := resource.DeleteResponse{State: state}
	implementation.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("denied peer removal must prevent Terraform's successful-delete state transition")
	}
	var retained programRepositoryBindingModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if retained.ID.ValueString() != "hidden" {
		t.Fatalf("denied removal lost state: %v", retained.ID)
	}

	readResponse := resource.ReadResponse{State: state}
	implementation.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if !readResponse.Diagnostics.HasError() {
		t.Fatal("hidden repository peer read must retain state with an error")
	}
	var readRetained programRepositoryBindingModel
	if diagnostics := readResponse.State.Get(context.Background(), &readRetained); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if readRetained.ID.ValueString() != "hidden" {
		t.Fatal("hidden repository peer was reported absent")
	}
	control := &controlTestLinkResource{client: client}
	controlSchema := resource.SchemaResponse{}
	control.Schema(context.Background(), resource.SchemaRequest{}, &controlSchema)
	controlState := tfsdk.State{Schema: controlSchema.Schema}
	controlModel := controlTestLinkModel{ID: types.StringValue("control-test"), ControlID: types.StringValue("hidden")}
	if diagnostics := controlState.Set(context.Background(), &controlModel); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	controlResponse := resource.ReadResponse{State: controlState}
	control.Read(context.Background(), resource.ReadRequest{State: controlState}, &controlResponse)
	if !controlResponse.Diagnostics.HasError() {
		t.Fatal("hidden control peer read must retain state with an error")
	}
	var retainedControl controlTestLinkModel
	if diagnostics := controlResponse.State.Get(context.Background(), &retainedControl); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if retainedControl.ID.ValueString() != "control-test" {
		t.Fatal("hidden control peer was reported absent")
	}
	if !edgeExists {
		t.Fatal("denied removal changed the owner edge")
	}

}
