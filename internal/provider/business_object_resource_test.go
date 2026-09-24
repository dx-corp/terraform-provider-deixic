package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestBusinessObjectImportIDRoundTrip(t *testing.T) {
	wantOrganization := "org/with punctuation"
	wantWorkspace := "workspace:with.dots"
	wantObject := "business_object_ä"
	encoded := encodeBusinessObjectImportID(wantOrganization, wantWorkspace, wantObject)
	organization, workspace, object, err := decodeBusinessObjectImportID(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if organization != wantOrganization || workspace != wantWorkspace || object != wantObject {
		t.Fatalf("round trip mismatch: %q %q %q", organization, workspace, object)
	}
}

func TestBusinessObjectImportIDRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "v1.only.two", "v2.YQ.Yg.Yw", "v1.!.Yg.Yw"} {
		if _, _, _, err := decodeBusinessObjectImportID(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

func TestFieldValueRoundTripAllKinds(t *testing.T) {
	tests := []*publicv1.BusinessFieldValue{
		{FieldId: "text", Value: &publicv1.BusinessFieldValue_Text{Text: ""}},
		{FieldId: "integer", Value: &publicv1.BusinessFieldValue_Integer{Integer: 0}},
		{FieldId: "boolean", Value: &publicv1.BusinessFieldValue_Boolean{Boolean: false}},
		{FieldId: "decimal", Value: &publicv1.BusinessFieldValue_Decimal{Decimal: "12.50"}},
		{FieldId: "money", Value: &publicv1.BusinessFieldValue_Money{Money: &publicv1.BusinessMoney{Amount: "5.00", Currency: "USD"}}},
		{FieldId: "enum", Value: &publicv1.BusinessFieldValue_EnumValue{EnumValue: "ACTIVE"}},
		{FieldId: "date", Value: &publicv1.BusinessFieldValue_Date{Date: "2026-09-18"}},
		{FieldId: "timestamp", Value: &publicv1.BusinessFieldValue_Timestamp{Timestamp: "2026-09-18T18:00:00Z"}},
		{FieldId: "reference", Value: &publicv1.BusinessFieldValue_Reference{Reference: &publicv1.BusinessObjectReference{ObjectId: "object-1"}}},
		{FieldId: "artifact", Value: &publicv1.BusinessFieldValue_Artifact{Artifact: &publicv1.BusinessArtifactReference{ArtifactVersionId: "artifact-version-1"}}},
		{FieldId: "list", Value: &publicv1.BusinessFieldValue_TextList{TextList: &publicv1.BusinessTextList{Values: []string{"a", "b"}}}},
	}
	for _, test := range tests {
		t.Run(test.GetFieldId(), func(t *testing.T) {
			model, err := fieldValueFromProto(context.Background(), test)
			if err != nil {
				t.Fatal(err)
			}
			got, err := fieldValueToProto(context.Background(), test.GetFieldId(), model)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != test.String() {
				t.Fatalf("round trip mismatch:\nwant %s\ngot  %s", test, got)
			}
		})
	}
}

func TestChangedProviderBindingMakesNoOwnerRequest(t *testing.T) {
	var calls atomic.Int64
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("request must not be sent")
	})}
	client, err := NewClient("https://api.example.com/platform", "token", "org-current", "workspace-current", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	baseState := businessObjectModel{
		APIEndpoint:    types.StringValue(client.Endpoint()),
		OrganizationID: types.StringValue(client.OrganizationID()),
		WorkspaceID:    types.StringValue(client.WorkspaceID()),
	}
	tests := []struct {
		name  string
		state businessObjectModel
	}{
		{name: "endpoint", state: func() businessObjectModel {
			value := baseState
			value.APIEndpoint = types.StringValue("https://other.example.com")
			return value
		}()},
		{name: "organization", state: func() businessObjectModel {
			value := baseState
			value.OrganizationID = types.StringValue("org-other")
			return value
		}()},
		{name: "workspace", state: func() businessObjectModel {
			value := baseState
			value.WorkspaceID = types.StringValue("workspace-other")
			return value
		}()},
	}
	implementation := &businessObjectResource{client: client}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := implementation.invokeForState(
				context.Background(),
				test.state,
				"GetBusinessObject",
				&publicv1.GetBusinessObjectRequest{},
				&publicv1.GetBusinessObjectResponse{},
			)
			if err == nil || !strings.Contains(err.Error(), "resource state belongs to") {
				t.Fatalf("expected state binding error, got %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("changed provider binding made %d owner requests", calls.Load())
	}
}

func TestDeleteResponseRequiresOwnerTombstone(t *testing.T) {
	request := &publicv1.DeleteBusinessObjectRequest{ObjectId: "business_object_1", ExpectedRevision: 4}
	object := &publicv1.BusinessObject{
		ObjectId:       request.GetObjectId(),
		TypeId:         "customer",
		SchemaRevision: 1,
		Revision:       5,
	}
	if err := validateDeleteResponse(request, object); err == nil || !strings.Contains(err.Error(), "did not confirm deletion") {
		t.Fatalf("expected missing tombstone error, got %v", err)
	}
	object.Deleted = true
	if err := validateDeleteResponse(request, object); err != nil {
		t.Fatalf("valid owner tombstone rejected: %v", err)
	}
}

func TestOwnerResponsesMustMatchRequestedIdentity(t *testing.T) {
	readRequest := &publicv1.GetBusinessObjectRequest{ObjectId: "business_object_expected"}
	object := &publicv1.BusinessObject{
		ObjectId:       "business_object_other",
		TypeId:         "customer",
		SchemaRevision: 1,
		Revision:       1,
	}
	state := businessObjectModel{TypeID: types.StringValue("customer")}
	if err := validateReadResponse(state, readRequest, object); err == nil || !strings.Contains(err.Error(), "requested object") {
		t.Fatalf("expected owner identity mismatch, got %v", err)
	}
	createRequest := &publicv1.CreateBusinessObjectRequest{TypeId: "customer", SchemaRevision: 1}
	object.ObjectId = ""
	if err := validateCreateResponse(createRequest, object); err == nil || !strings.Contains(err.Error(), "without an ID") {
		t.Fatalf("expected missing owner ID error, got %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestAccBusinessObjectLifecycleAndImport(t *testing.T) {
	fixture := newContractServer(t)
	server := httptestServer(t, fixture)
	providerFactories := testProviderFactories(server.Client())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if !fixture.allDeleted() {
				return errors.New("fixture contains an undeleted object")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: testBusinessObjectConfig(server.URL, fixture, "Acme", 2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("deixic_business_object.test", "id"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "type_id", "customer"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "schema_revision", "1"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "revision", "1"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "values.name.text", "Acme"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "values.seats.integer", "2"),
				),
			},
			{
				Config: testBusinessObjectConfig(server.URL, fixture, "Acme Corp", 3),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("deixic_business_object.test", "revision", "2"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "values.name.text", "Acme Corp"),
					resource.TestCheckResourceAttr("deixic_business_object.test", "values.seats.integer", "3"),
				),
			},
			{
				ResourceName:      "deixic_business_object.test",
				ImportState:       true,
				ImportStateIdFunc: businessObjectImportIDFromState(fixture),
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccBusinessObjectRefreshRemovesTombstone(t *testing.T) {
	fixture := newContractServer(t)
	server := httptestServer(t, fixture)
	providerFactories := testProviderFactories(server.Client())
	var objectID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: testBusinessObjectConfig(server.URL, fixture, "Deleted externally", 1),
				Check: func(state *terraform.State) error {
					objectID = state.RootModule().Resources["deixic_business_object.test"].Primary.ID
					return nil
				},
			},
			{
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				PreConfig: func() {
					fixture.markDeleted(objectID)
				},
				Check: func(state *terraform.State) error {
					if _, exists := state.RootModule().Resources["deixic_business_object.test"]; exists {
						return errors.New("tombstoned object remains in Terraform state")
					}
					return nil
				},
			},
		},
	})
}

func TestAccProviderRejectsBadToken(t *testing.T) {
	fixture := newContractServer(t)
	server := httptestServer(t, fixture)
	providerFactories := testProviderFactories(server.Client())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{{
			Config:      testBusinessObjectConfigWithToken(server.URL, fixture, "wrong-token", "Denied", 1),
			ExpectError: regexp.MustCompile(`Deixic API returned unauthenticated`),
		}},
	})
}

func testProviderFactories(client *http.Client) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		providerTypeName: providerserver.NewProtocol6WithError(&deixicProvider{version: "test", httpClient: client}),
	}
}

func httptestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func testBusinessObjectConfig(endpoint string, fixture *contractServer, name string, seats int) string {
	return testBusinessObjectConfigWithToken(endpoint, fixture, fixture.token, name, seats)
}

func testBusinessObjectConfigWithToken(endpoint string, fixture *contractServer, token, name string, seats int) string {
	return fmt.Sprintf(`
provider "deixic" {
  endpoint        = %q
  token           = %q
  organization_id = %q
  workspace_id    = %q
}

resource "deixic_business_object" "test" {
  type_id         = "customer"
  schema_revision = 1
  values = {
    name = { text = %q }
    seats = { integer = %d }
    active = { boolean = true }
    tags = { text_list = ["terraform", "managed"] }
    budget = {
      money_amount   = "25.00"
      money_currency = "USD"
    }
  }
}
`, endpoint, token, fixture.organizationID, fixture.workspaceID, name, seats)
}

func businessObjectImportIDFromState(fixture *contractServer) resource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		object := state.RootModule().Resources["deixic_business_object.test"]
		return encodeBusinessObjectImportID(fixture.organizationID, fixture.workspaceID, object.Primary.ID), nil
	}
}
