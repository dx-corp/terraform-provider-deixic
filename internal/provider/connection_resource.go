package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource              = (*connectionResource)(nil)
	_ resource.ResourceWithConfigure = (*connectionResource)(nil)
)

const managedExchangePath = "/v1/managed-provider-exchanges"

type connectionResource struct{ client *Client }

type connectionModel struct {
	ID                 types.String `tfsdk:"id"`
	ProviderID         types.String `tfsdk:"provider_id"`
	ConnectionID       types.String `tfsdk:"connection_id"`
	DisplayName        types.String `tfsdk:"display_name"`
	Profile            types.String `tfsdk:"profile"`
	RegistrationID     types.String `tfsdk:"registration_id"`
	ActorID            types.String `tfsdk:"actor_id"`
	ServicePrincipalID types.String `tfsdk:"service_principal_id"`
	AccountID          types.String `tfsdk:"account_id"`
	Scopes             types.List   `tfsdk:"scopes"`
	GCPIdentity        types.Object `tfsdk:"gcp_identity"`
	CredentialRefs     types.Map    `tfsdk:"credential_refs"`
	IssuedRefs         types.Map    `tfsdk:"issued_credential_refs"`
	ExpiresAtUnix      types.Int64  `tfsdk:"expires_at_unix"`
}

type gcpIdentityModel struct {
	ServiceAccountEmail      types.String `tfsdk:"service_account_email"`
	ScopeType                types.String `tfsdk:"scope_type"`
	ScopeID                  types.String `tfsdk:"scope_id"`
	WorkloadIdentityProvider types.String `tfsdk:"workload_identity_provider"`
	DurationSeconds          types.Int64  `tfsdk:"duration_seconds"`
}

func NewConnectionResource() resource.Resource { return &connectionResource{} }

func (r *connectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection"
}

func (r *connectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A connector enrollment through the connectors service managed exchange, for example a Google Cloud workload-identity connection. " +
			"Credentials are references only: no secret is accepted or stored in state. " +
			"The connectors service exposes enrollment but no read or delete here, so every attribute forces replacement and destroy only removes the resource from state.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Computed: true, Description: "Connection ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"provider_id":          schema.StringAttribute{Required: true, Description: "Connector provider, for example gcp.", PlanModifiers: replace},
			"connection_id":        schema.StringAttribute{Required: true, PlanModifiers: replace},
			"display_name":         schema.StringAttribute{Required: true, PlanModifiers: replace},
			"profile":              schema.StringAttribute{Required: true, Description: "Published exchange profile, for example gcp_workload_identity.", PlanModifiers: replace},
			"registration_id":      schema.StringAttribute{Required: true, PlanModifiers: replace},
			"actor_id":             schema.StringAttribute{Required: true, Description: "Principal enrolling the connection; must be the token's subject.", PlanModifiers: replace},
			"service_principal_id": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("connectors-oauth-exchange"), PlanModifiers: replace},
			"account_id":           schema.StringAttribute{Required: true, PlanModifiers: replace},
			"scopes":               schema.ListAttribute{Required: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()}},
			"gcp_identity": schema.SingleNestedAttribute{
				Optional:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"service_account_email":      schema.StringAttribute{Required: true},
					"scope_type":                 schema.StringAttribute{Required: true, Description: "projects, folders or organizations."},
					"scope_id":                   schema.StringAttribute{Required: true},
					"workload_identity_provider": schema.StringAttribute{Required: true},
					"duration_seconds":           schema.Int64Attribute{Required: true},
				},
			},
			"credential_refs": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				Description:   "Credential references by field name. Values are references to secrets held elsewhere, never secret values.",
				PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"issued_credential_refs": schema.MapAttribute{Computed: true, ElementType: types.StringType, Description: "References the exchange issued."},
			"expires_at_unix":        schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *connectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *connectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := r.client
	var scopes []string
	resp.Diagnostics.Append(plan.Scopes.ElementsAs(ctx, &scopes, false)...)
	refs := map[string]string{}
	resp.Diagnostics.Append(plan.CredentialRefs.ElementsAs(ctx, &refs, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	binding := map[string]any{
		"organizationId":     c.organizationID,
		"workspaceId":        c.workspaceID,
		"providerId":         plan.ProviderID.ValueString(),
		"connectionId":       plan.ConnectionID.ValueString(),
		"profile":            plan.Profile.ValueString(),
		"registrationId":     plan.RegistrationID.ValueString(),
		"actorId":            plan.ActorID.ValueString(),
		"servicePrincipalId": plan.ServicePrincipalID.ValueString(),
		"accountId":          plan.AccountID.ValueString(),
		"scopes":             scopes,
	}
	if !plan.GCPIdentity.IsNull() {
		var gcp gcpIdentityModel
		resp.Diagnostics.Append(plan.GCPIdentity.As(ctx, &gcp, basetypes.ObjectAsOptions{})...)
		binding["gcpIdentity"] = map[string]any{
			"serviceAccountEmail":      gcp.ServiceAccountEmail.ValueString(),
			"scopeType":                gcp.ScopeType.ValueString(),
			"scopeId":                  gcp.ScopeID.ValueString(),
			"workloadIdentityProvider": gcp.WorkloadIdentityProvider.ValueString(),
			"durationSeconds":          gcp.DurationSeconds.ValueInt64(),
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	request := map[string]any{
		"exchange":       map[string]any{"binding": binding, "credentialRefs": refs},
		"displayName":    plan.DisplayName.ValueString(),
		"idempotencyKey": stableKey("connection", c.organizationID, c.workspaceID, plan.ConnectionID.ValueString()),
	}
	var exchanged struct {
		ConnectionID   string            `json:"connectionId"`
		CredentialRefs map[string]string `json:"credentialRefs"`
		ExpiresAtUnix  int64             `json:"expiresAtUnix"`
	}
	if err := c.InvokeConnectors(ctx, managedExchangePath, request, &exchanged); err != nil {
		resp.Diagnostics.AddError("Enroll Deixic connection", err.Error())
		return
	}
	issued, d := types.MapValueFrom(ctx, types.StringType, exchanged.CredentialRefs)
	resp.Diagnostics.Append(d...)
	plan.ID = types.StringValue(exchanged.ConnectionID)
	plan.IssuedRefs = issued
	plan.ExpiresAtUnix = types.Int64Value(exchanged.ExpiresAtUnix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Read(_ context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {
}

func (r *connectionResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Every attribute forces replacement.")
}

func (r *connectionResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
