package provider

import (
	"context"
	"errors"
	"strconv"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = (*programResource)(nil)
	_ resource.ResourceWithConfigure = (*programResource)(nil)
)

const frameworksRelationship = "frameworks"

type programResource struct{ client *Client }

type programModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Goal         types.String `tfsdk:"goal"`
	OwnerLabel   types.String `tfsdk:"owner_label"`
	DueDate      types.String `tfsdk:"due_date"`
	FrameworkIDs types.Set    `tfsdk:"framework_ids"`
	Revision     types.Int64  `tfsdk:"revision"`
}

func NewProgramResource() resource.Resource { return &programResource{} }

func (r *programResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_program"
}

func (r *programResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A compliance program, for example \"dx-corp SOC 2\", and the frameworks it tracks.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Program ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true},
			"goal":        schema.StringAttribute{Optional: true, Computed: true},
			"owner_label": schema.StringAttribute{Optional: true, Computed: true, Description: "Authored coordination label, not an access grant."},
			"due_date":    schema.StringAttribute{Optional: true, Computed: true},
			"framework_ids": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Framework record IDs the program covers; see the deixic_frameworks data source.",
			},
			"revision": schema.Int64Attribute{Computed: true, Description: "Owner-assigned revision of the program record."},
		},
	}
}

func (r *programResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *programResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan programModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	response := &publicv1.CreateProgramResponse{}
	err := c.Invoke(ctx, "CreateProgram", &publicv1.CreateProgramRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("program", c.organizationID, c.workspaceID, plan.Name.ValueString()),
		Name:           plan.Name.ValueString(),
		Goal:           plan.Goal.ValueString(),
		OwnerLabel:     plan.OwnerLabel.ValueString(),
		DueDate:        plan.DueDate.ValueString(),
	}, response)
	if err != nil {
		resp.Diagnostics.AddError("Create Deixic program", err.Error())
		return
	}
	id := response.GetProgram().GetProgramId()
	if id == "" {
		resp.Diagnostics.AddError("Create Deixic program", "the owner returned a program without an ID")
		return
	}
	plan.ID = types.StringValue(id)
	var wanted []string
	resp.Diagnostics.Append(plan.FrameworkIDs.ElementsAs(ctx, &wanted, false)...)
	for _, frameworkID := range wanted {
		if err := c.relateFramework(ctx, id, frameworkID, false); err != nil {
			resp.Diagnostics.AddError("Link program framework", err.Error())
			return
		}
	}
	r.refresh(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *programResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state programModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.getProgram(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read Deixic program", err.Error())
		return
	}
	r.refresh(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *programResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state programModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	id := state.ID.ValueString()
	program, err := c.getProgram(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Read Deixic program", err.Error())
		return
	}
	if plan.Name != state.Name || plan.Goal != state.Goal || plan.OwnerLabel != state.OwnerLabel || plan.DueDate != state.DueDate {
		err = c.Invoke(ctx, "UpdateProgram", &publicv1.UpdateProgramRequest{
			OrganizationId:   c.organizationID,
			WorkspaceId:      c.workspaceID,
			IdempotencyKey:   stableKey("program-update", id, strconv.FormatInt(program.GetRevision(), 10), plan.Name.ValueString(), plan.Goal.ValueString(), plan.OwnerLabel.ValueString(), plan.DueDate.ValueString()),
			ProgramId:        id,
			ExpectedRevision: program.GetRevision(),
			Name:             plan.Name.ValueString(),
			Goal:             plan.Goal.ValueString(),
			OwnerLabel:       plan.OwnerLabel.ValueString(),
			DueDate:          plan.DueDate.ValueString(),
			// The owner requires a status; Terraform does not manage it, so keep the current one.
			PlanningStatus: program.GetPlanningStatus(),
		}, &publicv1.UpdateProgramResponse{})
		if err != nil {
			resp.Diagnostics.AddError("Update Deixic program", err.Error())
			return
		}
	}
	var wanted, current []string
	resp.Diagnostics.Append(plan.FrameworkIDs.ElementsAs(ctx, &wanted, false)...)
	resp.Diagnostics.Append(state.FrameworkIDs.ElementsAs(ctx, &current, false)...)
	for _, frameworkID := range difference(wanted, current) {
		if err := c.relateFramework(ctx, id, frameworkID, false); err != nil {
			resp.Diagnostics.AddError("Link program framework", err.Error())
			return
		}
	}
	for _, frameworkID := range difference(current, wanted) {
		if err := c.relateFramework(ctx, id, frameworkID, true); err != nil {
			resp.Diagnostics.AddError("Unlink program framework", err.Error())
			return
		}
	}
	plan.ID = state.ID
	r.refresh(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *programResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state programModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	id := state.ID.ValueString()
	program, err := c.getProgram(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read Deixic program", err.Error())
		return
	}
	var linked []string
	resp.Diagnostics.Append(state.FrameworkIDs.ElementsAs(ctx, &linked, false)...)
	for _, frameworkID := range linked {
		if err := c.relateFramework(ctx, id, frameworkID, true); err != nil {
			resp.Diagnostics.AddError("Unlink program framework", err.Error())
			return
		}
	}
	// Relationship mutations advance the same source record revision.
	program, err = c.getProgram(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read Deixic program after unlinking frameworks", err.Error())
		return
	}
	err = c.Invoke(ctx, "DeleteBusinessObject", &publicv1.DeleteBusinessObjectRequest{
		OrganizationId:   c.organizationID,
		WorkspaceId:      c.workspaceID,
		IdempotencyKey:   stableKey("program-delete", id),
		ObjectId:         id,
		ExpectedRevision: program.GetRevision(),
	}, &publicv1.DeleteBusinessObjectResponse{})
	if err != nil && !errors.Is(err, ErrNotFound) {
		resp.Diagnostics.AddError("Delete Deixic program", err.Error())
	}
}

// refresh reads the program and its linked frameworks into the model.
func (r *programResource) refresh(ctx context.Context, model *programModel, diagnostics *diag.Diagnostics) {
	program, err := r.client.getProgram(ctx, model.ID.ValueString())
	if err != nil {
		diagnostics.AddError("Read Deixic program", err.Error())
		return
	}
	linked, err := r.client.listRelated(ctx, model.ID.ValueString(), frameworksRelationship)
	if err != nil {
		diagnostics.AddError("Read program frameworks", err.Error())
		return
	}
	if linked == nil {
		linked = []string{}
	}
	model.Name = types.StringValue(program.GetName())
	model.Goal = types.StringValue(program.GetGoal())
	model.OwnerLabel = types.StringValue(program.GetOwnerLabel())
	model.DueDate = types.StringValue(program.GetDueDate())
	model.Revision = types.Int64Value(program.GetRevision())
	if len(linked) == 0 && model.FrameworkIDs.IsNull() {
		return
	}
	set, d := types.SetValueFrom(ctx, types.StringType, linked)
	diagnostics.Append(d...)
	model.FrameworkIDs = set
}

func (c *Client) getProgram(ctx context.Context, id string) (*publicv1.Program, error) {
	response := &publicv1.GetProgramResponse{}
	err := c.Invoke(ctx, "GetProgram", &publicv1.GetProgramRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		ProgramId:      id,
	}, response)
	return response.GetProgram(), err
}

// relateFramework creates or removes the program -> frameworks -> framework link.
func (c *Client) relateFramework(ctx context.Context, programID, frameworkID string, remove bool) error {
	return c.setRelationship(ctx, programID, frameworksRelationship, frameworkID, remove)
}

// listRelated returns the target IDs of one relationship kind.
func (c *Client) listRelated(ctx context.Context, objectID, relationshipID string) ([]string, error) {
	var targets []string
	after := ""
	for {
		response := &publicv1.ListBusinessObjectRelationshipsResponse{}
		err := c.Invoke(ctx, "ListBusinessObjectRelationships", &publicv1.ListBusinessObjectRelationshipsRequest{
			OrganizationId: c.organizationID,
			WorkspaceId:    c.workspaceID,
			ObjectId:       objectID,
			RelationshipId: relationshipID,
			Limit:          recordPageSize,
			AfterKey:       after,
		}, response)
		if err != nil {
			return nil, err
		}
		for _, relationship := range response.GetRelationships() {
			targets = append(targets, relationship.GetTargetObjectId())
		}
		after = response.GetNextKey()
		if after == "" {
			return targets, nil
		}
	}
}

// difference returns the members of a that are not in b.
func difference(a, b []string) []string {
	seen := map[string]bool{}
	for _, v := range b {
		seen[v] = true
	}
	var out []string
	for _, v := range a {
		if !seen[v] {
			out = append(out, v)
		}
	}
	return out
}
