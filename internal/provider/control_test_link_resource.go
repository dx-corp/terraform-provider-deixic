package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = (*controlTestLinkResource)(nil)
	_ resource.ResourceWithConfigure = (*controlTestLinkResource)(nil)
)

const controlsRelationship = "controls"

type controlTestLinkResource struct{ client *Client }

type controlTestLinkModel struct {
	ID               types.String `tfsdk:"id"`
	ControlKey       types.String `tfsdk:"control_key"`
	ControlID        types.String `tfsdk:"control_id"`
	TestID           types.String `tfsdk:"test_id"`
	ConnectionID     types.String `tfsdk:"connection_id"`
	Name             types.String `tfsdk:"name"`
	Source           types.String `tfsdk:"source"`
	Aggregation      types.String `tfsdk:"aggregation"`
	ControlTestType  types.String `tfsdk:"control_test_type_id"`
	RepositoryTypeID types.String `tfsdk:"repository_type_id"`
}

func NewControlTestLinkResource() resource.Resource { return &controlTestLinkResource{} }

func (r *controlTestLinkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_control_test_link"
}

func (r *controlTestLinkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resolved := []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Links a native control test to a control, for example the default-branch-protection test to DX-CHG-03. " +
			"The test applies to every repository record. Destroy unlinks and deletes the test record.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Computed: true, Description: "Control test record ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"control_key":          schema.StringAttribute{Required: true, Description: "Key of the control, for example DX-CHG-03.", PlanModifiers: replace},
			"control_id":           schema.StringAttribute{Computed: true, PlanModifiers: resolved},
			"test_id":              schema.StringAttribute{Required: true, Description: "Native test identifier, for example dx.test.repository.default_branch_protection/v1.", PlanModifiers: replace},
			"connection_id":        schema.StringAttribute{Required: true, Description: "Connection whose evidence the test reads.", PlanModifiers: replace},
			"name":                 schema.StringAttribute{Required: true, PlanModifiers: replace},
			"source":               schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("github"), Description: "Provider prefix of the test key.", PlanModifiers: replace},
			"aggregation":          schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("all"), Description: "How member results combine: all or any as published by the schema.", PlanModifiers: replace},
			"control_test_type_id": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: resolved},
			"repository_type_id":   schema.StringAttribute{Optional: true, Computed: true, Description: "Type of the records the test applies to.", PlanModifiers: resolved},
		},
	}
}

func (r *controlTestLinkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *controlTestLinkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan controlTestLinkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	testType, err := c.resolveTypeID(ctx, plan.ControlTestType.ValueString(), "control_test")
	if err != nil {
		resp.Diagnostics.AddError("Resolve control test type", err.Error())
		return
	}
	repoType, err := c.resolveTypeID(ctx, plan.RepositoryTypeID.ValueString(), "repository")
	if err != nil {
		resp.Diagnostics.AddError("Resolve repository type", err.Error())
		return
	}
	controlID, err := c.findControl(ctx, plan.ControlKey.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Find control", err.Error())
		return
	}
	population, err := json.Marshal(map[string]any{"type_id": repoType, "applicability_state": "applies", "missing_scope_facts": []string{}})
	if err != nil {
		resp.Diagnostics.AddError("Encode population", err.Error())
		return
	}
	key := fmt.Sprintf("%s:%s:%s", plan.Source.ValueString(), plan.ConnectionID.ValueString(), plan.TestID.ValueString())
	created := &publicv1.CreateBusinessObjectResponse{}
	err = c.Invoke(ctx, "CreateBusinessObject", &publicv1.CreateBusinessObjectRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("control-test", c.organizationID, c.workspaceID, key),
		TypeId:         testType,
		SchemaRevision: 1,
		Values: []*publicv1.BusinessFieldValue{
			textField("key", key),
			textField("name", plan.Name.ValueString()),
			textField("test_id", plan.TestID.ValueString()),
			textField("population", string(population)),
			{FieldId: "aggregation", Value: &publicv1.BusinessFieldValue_EnumValue{EnumValue: plan.Aggregation.ValueString()}},
		},
	}, created)
	if err != nil {
		resp.Diagnostics.AddError("Create control test", err.Error())
		return
	}
	testObject := created.GetObject().GetObjectId()
	err = c.Invoke(ctx, "CreateBusinessObjectRelationship", &publicv1.CreateBusinessObjectRelationshipRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("control-test-control", testObject, controlID),
		Relationship:   &publicv1.BusinessObjectRelationship{SourceObjectId: testObject, RelationshipId: controlsRelationship, TargetObjectId: controlID},
	}, &publicv1.CreateBusinessObjectRelationshipResponse{})
	if err != nil {
		resp.Diagnostics.AddError("Link control test to control", err.Error())
		return
	}
	plan.ID = types.StringValue(testObject)
	plan.ControlID = types.StringValue(controlID)
	plan.ControlTestType = types.StringValue(testType)
	plan.RepositoryTypeID = types.StringValue(repoType)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *controlTestLinkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state controlTestLinkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	object, err := r.client.getObject(ctx, state.ID.ValueString())
	if errors.Is(err, ErrNotFound) || object.GetDeleted() {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read control test", err.Error())
		return
	}
	linked, err := r.client.listRelated(ctx, state.ID.ValueString(), controlsRelationship)
	if err != nil {
		resp.Diagnostics.AddError("Read control test links", err.Error())
		return
	}
	if !slices.Contains(linked, state.ControlID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *controlTestLinkResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Every attribute forces replacement.")
}

func (r *controlTestLinkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state controlTestLinkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	err := c.Invoke(ctx, "DeleteBusinessObjectRelationship", &publicv1.DeleteBusinessObjectRelationshipRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("control-test-control-remove", state.ID.ValueString(), state.ControlID.ValueString()),
		Relationship:   &publicv1.BusinessObjectRelationship{SourceObjectId: state.ID.ValueString(), RelationshipId: controlsRelationship, TargetObjectId: state.ControlID.ValueString()},
	}, &publicv1.DeleteBusinessObjectRelationshipResponse{})
	if err != nil && !errors.Is(err, ErrNotFound) {
		resp.Diagnostics.AddError("Unlink control test", err.Error())
		return
	}
	object, err := c.getObject(ctx, state.ID.ValueString())
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read control test", err.Error())
		return
	}
	err = c.Invoke(ctx, "DeleteBusinessObject", &publicv1.DeleteBusinessObjectRequest{
		OrganizationId:   c.organizationID,
		WorkspaceId:      c.workspaceID,
		IdempotencyKey:   stableKey("control-test-delete", state.ID.ValueString()),
		ObjectId:         state.ID.ValueString(),
		ExpectedRevision: object.GetRevision(),
	}, &publicv1.DeleteBusinessObjectResponse{})
	if err != nil && !errors.Is(err, ErrNotFound) {
		resp.Diagnostics.AddError("Delete control test", err.Error())
	}
}

func (c *Client) getObject(ctx context.Context, id string) (*publicv1.BusinessObject, error) {
	response := &publicv1.GetBusinessObjectResponse{}
	err := c.Invoke(ctx, "GetBusinessObject", &publicv1.GetBusinessObjectRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		ObjectId:       id,
	}, response)
	return response.GetObject(), err
}

// findControl returns the record ID of the control with the exact key.
func (c *Client) findControl(ctx context.Context, key string) (string, error) {
	typeID, err := c.resolveTypeID(ctx, "", "control")
	if err != nil {
		return "", err
	}
	objects, err := c.listObjects(ctx, typeID)
	if err != nil {
		return "", err
	}
	for _, object := range objects {
		if fieldText(object, "key") == key {
			return object.GetObjectId(), nil
		}
	}
	return "", fmt.Errorf("no control has key %q; apply the compliance library first", key)
}
