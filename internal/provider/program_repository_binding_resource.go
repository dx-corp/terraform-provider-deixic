package provider

import (
	"context"
	"errors"
	"slices"

	consolev1 "github.com/dx-corp/terraform-provider-deixic/internal/consoleproto"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = (*programRepositoryBindingResource)(nil)
	_ resource.ResourceWithConfigure = (*programRepositoryBindingResource)(nil)
)

const (
	repositoriesRelationship = "repositories"
	defaultRepositoryFamily  = "resource_family:github.classic_protection.observed"
	defaultRepositoryRecord  = "classic_protection"
)

type programRepositoryBindingResource struct{ client *Client }

type programRepositoryBindingModel struct {
	ID               types.String `tfsdk:"id"`
	ProgramID        types.String `tfsdk:"program_id"`
	ConnectionID     types.String `tfsdk:"connection_id"`
	Repository       types.String `tfsdk:"repository"`
	GitHubRepoID     types.String `tfsdk:"github_repo_id"`
	FamilyStableID   types.String `tfsdk:"family_stable_id"`
	RecordSuffix     types.String `tfsdk:"record_suffix"`
	RepositoryTypeID types.String `tfsdk:"repository_type_id"`
}

func NewProgramRepositoryBindingResource() resource.Resource {
	return &programRepositoryBindingResource{}
}

func (r *programRepositoryBindingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_program_repository_binding"
}

func requiredReplaceString() schema.StringAttribute {
	return schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func (r *programRepositoryBindingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Binds one GitHub repository to a program. Creates the repository record, binds it to the connection's observed GitHub resource family, and links it to the program. " +
			"Destroy unlinks the repository from the program; source-bound records are retained.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Computed: true, Description: "Repository record ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"program_id":       requiredReplaceString(),
			"connection_id":    requiredReplaceString(),
			"repository":       requiredReplaceString(),
			"github_repo_id":   requiredReplaceString(),
			"family_stable_id": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(defaultRepositoryFamily), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"record_suffix": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(defaultRepositoryRecord),
				Description:   "Appended to github_repo_id to form the source record ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"repository_type_id": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}},
		},
	}
}

func (r *programRepositoryBindingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *programRepositoryBindingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan programRepositoryBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	typeID, err := c.resolveTypeID(ctx, plan.RepositoryTypeID.ValueString(), "repository")
	if err != nil {
		resp.Diagnostics.AddError("Resolve repository type", err.Error())
		return
	}
	repoKey := plan.ConnectionID.ValueString() + "/" + plan.GitHubRepoID.ValueString()
	created := &consolev1.CreateBusinessObjectResponse{}
	err = c.InvokeConsole(ctx, "CreateBusinessObject", &consolev1.CreateBusinessObjectRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("repository", c.organizationID, c.workspaceID, repoKey),
		TypeId:         typeID,
		SchemaRevision: 1,
		Values:         []*consolev1.BusinessFieldValue{textField("name", plan.Repository.ValueString())},
	}, created)
	if err != nil {
		resp.Diagnostics.AddError("Create repository record", err.Error())
		return
	}
	object := created.GetObject()
	if object.GetSource() == nil {
		bound := &consolev1.BindBusinessObjectSourceResponse{}
		err = c.InvokeConsole(ctx, "BindBusinessObjectSource", &consolev1.BindBusinessObjectSourceRequest{
			OrganizationId:   c.organizationID,
			WorkspaceId:      c.workspaceID,
			IdempotencyKey:   stableKey("repository-bind", c.organizationID, c.workspaceID, repoKey),
			ObjectId:         object.GetObjectId(),
			ExpectedRevision: object.GetRevision(),
			ConnectionId:     plan.ConnectionID.ValueString(),
			FamilyStableId:   plan.FamilyStableID.ValueString(),
			RecordId:         plan.GitHubRepoID.ValueString() + plan.RecordSuffix.ValueString(),
		}, bound)
		if err != nil {
			resp.Diagnostics.AddError("Bind repository to its GitHub source", err.Error())
			return
		}
		object = bound.GetObject()
	}
	err = c.InvokeConsole(ctx, "CreateBusinessObjectRelationship", &consolev1.CreateBusinessObjectRelationshipRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("program-repository", plan.ProgramID.ValueString(), object.GetObjectId()),
		Relationship: &consolev1.BusinessObjectRelationship{
			SourceObjectId: plan.ProgramID.ValueString(),
			RelationshipId: repositoriesRelationship,
			TargetObjectId: object.GetObjectId(),
		},
	}, &consolev1.CreateBusinessObjectRelationshipResponse{})
	if err != nil {
		resp.Diagnostics.AddError("Link repository to program", err.Error())
		return
	}
	plan.ID = types.StringValue(object.GetObjectId())
	plan.RepositoryTypeID = types.StringValue(typeID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *programRepositoryBindingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state programRepositoryBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	got := &consolev1.GetBusinessObjectResponse{}
	err := c.InvokeConsole(ctx, "GetBusinessObject", &consolev1.GetBusinessObjectRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		ObjectId:       state.ID.ValueString(),
	}, got)
	if errors.Is(err, ErrNotFound) || got.GetObject().GetDeleted() {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read repository record", err.Error())
		return
	}
	linked, err := c.listRelated(ctx, state.ProgramID.ValueString(), repositoriesRelationship)
	if err != nil {
		resp.Diagnostics.AddError("Read program repositories", err.Error())
		return
	}
	if !slices.Contains(linked, state.ID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *programRepositoryBindingResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Every attribute forces replacement.")
}

func (r *programRepositoryBindingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state programRepositoryBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	err := c.InvokeConsole(ctx, "DeleteBusinessObjectRelationship", &consolev1.DeleteBusinessObjectRelationshipRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("program-repository-remove", state.ProgramID.ValueString(), state.ID.ValueString()),
		Relationship: &consolev1.BusinessObjectRelationship{
			SourceObjectId: state.ProgramID.ValueString(),
			RelationshipId: repositoriesRelationship,
			TargetObjectId: state.ID.ValueString(),
		},
	}, &consolev1.DeleteBusinessObjectRelationshipResponse{})
	if err != nil && !errors.Is(err, ErrNotFound) {
		resp.Diagnostics.AddError("Unlink repository from program", err.Error())
	}
}
