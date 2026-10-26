package provider

import (
	"context"
	"encoding/json"
	"errors"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
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
	SourceGroupIDs   types.Set    `tfsdk:"source_group_ids"`
	SourceFields     types.Map    `tfsdk:"source_fields"`
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
			"repository_type_id": requiredReplaceString(),
			"source_group_ids":   schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Explicit source-owned group IDs of the repository type. Every selected field must support source mapping.", PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()}},
			"source_fields":      schema.MapAttribute{Required: true, ElementType: types.StringType, Description: "Maps each field in the selected groups to display_name or external_id. Contains references, never observed values.", PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()}},
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
	typeID := plan.RepositoryTypeID.ValueString()
	var groups []string
	mappings := map[string]string{}
	resp.Diagnostics.Append(plan.SourceGroupIDs.ElementsAs(ctx, &groups, false)...)
	resp.Diagnostics.Append(plan.SourceFields.ElementsAs(ctx, &mappings, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	intent, err := c.repositoryBindingIntent(ctx, typeID, groups, mappings)
	if err != nil {
		resp.Diagnostics.AddError("Validate repository source intent", err.Error())
		return
	}
	keyMaterial, err := json.Marshal(struct {
		OrganizationID string                                 `json:"organization_id"`
		WorkspaceID    string                                 `json:"workspace_id"`
		TypeID         string                                 `json:"type_id"`
		SchemaRevision int64                                  `json:"schema_revision"`
		DisplayFieldID string                                 `json:"display_field_id"`
		ConnectionID   string                                 `json:"connection_id"`
		GitHubRepoID   string                                 `json:"github_repo_id"`
		Repository     string                                 `json:"repository"`
		FamilyStableID string                                 `json:"family_stable_id"`
		RecordSuffix   string                                 `json:"record_suffix"`
		Groups         []string                               `json:"groups"`
		Fields         []*publicv1.BusinessSourceFieldMapping `json:"fields"`
	}{
		OrganizationID: c.organizationID, WorkspaceID: c.workspaceID, TypeID: typeID,
		SchemaRevision: intent.definition.GetRevision(), DisplayFieldID: intent.definition.GetDisplayFieldId(),
		ConnectionID: plan.ConnectionID.ValueString(), GitHubRepoID: plan.GitHubRepoID.ValueString(), Repository: plan.Repository.ValueString(),
		FamilyStableID: plan.FamilyStableID.ValueString(), RecordSuffix: plan.RecordSuffix.ValueString(), Groups: intent.groups, Fields: intent.fields,
	})
	if err != nil {
		resp.Diagnostics.AddError("Encode repository source intent", err.Error())
		return
	}
	repoKey := stableKey("repository-source-intent/v1", string(keyMaterial))

	created := &publicv1.CreateBusinessObjectResponse{}
	err = c.Invoke(ctx, "CreateBusinessObject", &publicv1.CreateBusinessObjectRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
		IdempotencyKey: stableKey("repository", c.organizationID, c.workspaceID, repoKey),
		TypeId:         typeID,
		SchemaRevision: intent.definition.GetRevision(),
		Values:         []*publicv1.BusinessFieldValue{textField(intent.definition.GetDisplayFieldId(), plan.Repository.ValueString())},
	}, created)
	if err != nil {
		resp.Diagnostics.AddError("Create repository record", err.Error())
		return
	}
	object, err := c.getObject(ctx, created.GetObject().GetObjectId())
	if err != nil {
		resp.Diagnostics.AddError("Read repository after creation", err.Error())
		return
	}
	if object.GetDeleted() || object.GetObjectId() != created.GetObject().GetObjectId() || object.GetTypeId() != typeID || object.GetSchemaRevision() != intent.definition.GetRevision() {
		resp.Diagnostics.AddError("Repository owner state changed", "The created record no longer matches the selected repository type and schema revision; reconcile the record before retrying.")
		return
	}
	// Reissue the exact owned binding command. The immutable create response
	// pins its original revision; current binding presence is not match proof.
	bound := &publicv1.BindBusinessObjectSourceResponse{}
	err = c.Invoke(ctx, "BindBusinessObjectSource", &publicv1.BindBusinessObjectSourceRequest{
		OrganizationId:   c.organizationID,
		WorkspaceId:      c.workspaceID,
		IdempotencyKey:   stableKey("repository-bind", c.organizationID, c.workspaceID, repoKey),
		ObjectId:         object.GetObjectId(),
		ExpectedRevision: created.GetObject().GetRevision(),
		ConnectionId:     plan.ConnectionID.ValueString(),
		FamilyStableId:   plan.FamilyStableID.ValueString(),
		RecordId:         plan.GitHubRepoID.ValueString() + plan.RecordSuffix.ValueString(),
		GroupIds:         intent.groups,
		Fields:           intent.fields,
	}, bound)
	if err != nil {
		resp.Diagnostics.AddError("Bind repository to its GitHub source", err.Error())
		return
	}
	object = bound.GetObject()
	err = c.setRelationship(ctx, plan.ProgramID.ValueString(), repositoriesRelationship, object.GetObjectId(), false)
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
	got := &publicv1.GetBusinessObjectResponse{}
	err := c.Invoke(ctx, "GetBusinessObject", &publicv1.GetBusinessObjectRequest{
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
	linked, err := c.relationshipExists(ctx, state.ProgramID.ValueString(), repositoriesRelationship, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read program repositories", err.Error())
		return
	}
	if !linked {
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
	err := c.setRelationship(ctx, state.ProgramID.ValueString(), repositoriesRelationship, state.ID.ValueString(), true)
	if err != nil && !errors.Is(err, ErrNotFound) {
		resp.Diagnostics.AddError("Unlink repository from program", err.Error())
	}
}
