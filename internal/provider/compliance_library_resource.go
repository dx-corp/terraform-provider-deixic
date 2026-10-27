package provider

import (
	"context"
	"fmt"
	"sort"
	"time"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource               = (*complianceLibraryResource)(nil)
	_ resource.ResourceWithConfigure  = (*complianceLibraryResource)(nil)
	_ resource.ResourceWithModifyPlan = (*complianceLibraryResource)(nil)
)

const libraryApplyDeadline = 45 * time.Minute

// libraryPollInterval separates repeated apply calls while the server job runs.
var libraryPollInterval = 3 * time.Second

type complianceLibraryResource struct{ client *Client }

type complianceLibraryModel struct {
	ID             types.String `tfsdk:"id"`
	BlueprintIDs   types.Set    `tfsdk:"blueprint_ids"`
	LibraryTypeID  types.String `tfsdk:"library_type_id"`
	Selections     types.List   `tfsdk:"selections"`
	PlanDigest     types.String `tfsdk:"plan_digest"`
	AppliedChanges types.Int64  `tfsdk:"applied_changes"`
	PendingChanges types.Int64  `tfsdk:"pending_changes"`
}

var selectionAttrTypes = map[string]attr.Type{
	"blueprint_id":   types.StringType,
	"version":        types.Int64Type,
	"content_digest": types.StringType,
}

func NewComplianceLibraryResource() resource.Resource { return &complianceLibraryResource{} }

func (r *complianceLibraryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_compliance_library"
}

func (r *complianceLibraryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The workspace's shared compliance library: the framework packs it contains. " +
			"Apply previews the plan, applies it against the previewed plan digest, and polls the server job until it commits. " +
			"Destroying the resource removes it from state and leaves the installed library in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Library clone ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"blueprint_ids": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Complete set of framework pack blueprint IDs, for example cerebro-framework-soc2. The newest catalog version of each is installed; packs omitted here are withdrawn.",
			},
			"library_type_id": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Type ID of the control_test type that owns the library. Resolved from the installed schemas when omitted.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"selections": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Exact pack versions and content digests that were applied.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"blueprint_id":   schema.StringAttribute{Computed: true},
					"version":        schema.Int64Attribute{Computed: true},
					"content_digest": schema.StringAttribute{Computed: true},
				}},
			},
			"plan_digest":     schema.StringAttribute{Computed: true, Description: "Digest of the reconcile plan that was applied."},
			"applied_changes": schema.Int64Attribute{Computed: true, Description: "Number of changes in the applied plan."},
			"pending_changes": schema.Int64Attribute{Computed: true, Description: "Changes a fresh preview would still make; non-zero means drift."},
		},
	}
}

func (r *complianceLibraryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan turns drift found by Read into an update.
func (r *complianceLibraryResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state complianceLibraryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.PendingChanges.ValueInt64() == 0 {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("plan_digest"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("applied_changes"), types.Int64Unknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("pending_changes"), types.Int64Unknown())...)
}

func (r *complianceLibraryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan complianceLibraryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *complianceLibraryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan complianceLibraryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *complianceLibraryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state complianceLibraryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	selections := selectionsFromList(ctx, state.Selections)
	preview, err := r.client.reconcileLibrary(ctx, state.LibraryTypeID.ValueString(), selections, "", "", publicv1.ComplianceLibraryReconcileMode_COMPLIANCE_LIBRARY_RECONCILE_MODE_PREVIEW)
	if err != nil {
		resp.Diagnostics.AddError("Preview Deixic compliance library", err.Error())
		return
	}
	state.PendingChanges = types.Int64Value(int64(len(preview.GetChanges())))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *complianceLibraryResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// reconcile resolves the packs, previews, applies against the previewed digest
// and polls until the server job commits.
func (r *complianceLibraryResource) reconcile(ctx context.Context, plan *complianceLibraryModel, diagnostics *diag.Diagnostics) {
	var ids []string
	if d := plan.BlueprintIDs.ElementsAs(ctx, &ids, false); d.HasError() {
		diagnostics.Append(d...)
		return
	}
	selections, err := r.client.resolveSelections(ctx, ids)
	if err != nil {
		diagnostics.AddError("Resolve compliance library packs", err.Error())
		return
	}
	typeID, err := r.client.resolveTypeID(ctx, plan.LibraryTypeID.ValueString(), "control_test")
	if err != nil {
		diagnostics.AddError("Resolve compliance library type", err.Error())
		return
	}
	preview, err := r.client.reconcileLibrary(ctx, typeID, selections, "", "", publicv1.ComplianceLibraryReconcileMode_COMPLIANCE_LIBRARY_RECONCILE_MODE_PREVIEW)
	if err != nil {
		diagnostics.AddError("Preview Deixic compliance library", err.Error())
		return
	}
	changes := int64(len(preview.GetChanges()))
	cloneID := preview.GetLibraryCloneId()
	if changes > 0 {
		applied, err := r.client.applyLibrary(ctx, typeID, selections, preview.GetPlanDigest())
		if err != nil {
			diagnostics.AddError("Apply Deixic compliance library", err.Error())
			return
		}
		cloneID = applied.GetLibraryCloneId()
	}
	list, d := selectionsToList(selections)
	if d.HasError() {
		diagnostics.Append(d...)
		return
	}
	plan.ID = types.StringValue(cloneID)
	plan.LibraryTypeID = types.StringValue(typeID)
	plan.Selections = list
	plan.PlanDigest = types.StringValue(preview.GetPlanDigest())
	plan.AppliedChanges = types.Int64Value(changes)
	plan.PendingChanges = types.Int64Value(0)
}

// resolveSelections pins each blueprint ID to its newest catalog version and digest.
func (c *Client) resolveSelections(ctx context.Context, ids []string) ([]*publicv1.BlueprintSelection, error) {
	blueprints, err := c.listBlueprints(ctx, "")
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	selections := make([]*publicv1.BlueprintSelection, 0, len(ids))
	for _, id := range ids {
		var best *publicv1.BusinessBlueprint
		for _, blueprint := range blueprints {
			if blueprint.GetBlueprintId() == id && (best == nil || blueprint.GetVersion() > best.GetVersion()) {
				best = blueprint
			}
		}
		if best == nil {
			return nil, fmt.Errorf("blueprint %q is not in the catalog", id)
		}
		selections = append(selections, &publicv1.BlueprintSelection{
			BlueprintId:   best.GetBlueprintId(),
			Version:       best.GetVersion(),
			ContentDigest: best.GetContentDigest(),
		})
	}
	return selections, nil
}

func (c *Client) reconcileLibrary(ctx context.Context, typeID string, selections []*publicv1.BlueprintSelection, key, expectedDigest string, mode publicv1.ComplianceLibraryReconcileMode) (*publicv1.ReconcileComplianceLibraryResponse, error) {
	response := &publicv1.ReconcileComplianceLibraryResponse{}
	err := c.Invoke(ctx, "ReconcileComplianceLibrary", &publicv1.ReconcileComplianceLibraryRequest{
		OrganizationId:     c.organizationID,
		WorkspaceId:        c.workspaceID,
		IdempotencyKey:     key,
		LibraryTypeId:      typeID,
		Selections:         selections,
		ExpectedPlanDigest: expectedDigest,
		Mode:               mode,
	}, response)
	return response, err
}

// applyLibrary applies the previewed plan. The key derives from the plan, so a
// retry or a repeated call joins the same server job. The server answers
// applied=false while that job runs; the identical request is the poll.
func (c *Client) applyLibrary(ctx context.Context, typeID string, selections []*publicv1.BlueprintSelection, planDigest string) (*publicv1.ReconcileComplianceLibraryResponse, error) {
	parts := []string{"library-apply", c.organizationID, c.workspaceID, typeID, planDigest}
	for _, selection := range selections {
		parts = append(parts, selection.GetBlueprintId(), selection.GetContentDigest())
	}
	key := stableKey(parts...)
	ctx, cancel := context.WithTimeout(ctx, libraryApplyDeadline)
	defer cancel()
	for {
		response, err := c.reconcileLibrary(ctx, typeID, selections, key, planDigest, publicv1.ComplianceLibraryReconcileMode_COMPLIANCE_LIBRARY_RECONCILE_MODE_APPLY)
		if err != nil {
			return nil, err
		}
		if response.GetApplied() {
			return response, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("library apply still running after %s; run apply again to keep waiting (plan %s)", libraryApplyDeadline, planDigest)
		case <-time.After(libraryPollInterval):
		}
	}
}

func selectionsToList(selections []*publicv1.BlueprintSelection) (types.List, diag.Diagnostics) {
	objectType := types.ObjectType{AttrTypes: selectionAttrTypes}
	values := make([]attr.Value, 0, len(selections))
	for _, selection := range selections {
		values = append(values, types.ObjectValueMust(selectionAttrTypes, map[string]attr.Value{
			"blueprint_id":   types.StringValue(selection.GetBlueprintId()),
			"version":        types.Int64Value(selection.GetVersion()),
			"content_digest": types.StringValue(selection.GetContentDigest()),
		}))
	}
	return types.ListValue(objectType, values)
}

func selectionsFromList(_ context.Context, list types.List) []*publicv1.BlueprintSelection {
	var selections []*publicv1.BlueprintSelection
	for _, element := range list.Elements() {
		object, _ := element.(basetypes.ObjectValue)
		attrs := object.Attributes()
		selections = append(selections, &publicv1.BlueprintSelection{
			BlueprintId:   stringAttr(attrs["blueprint_id"]),
			Version:       int64Attr(attrs["version"]),
			ContentDigest: stringAttr(attrs["content_digest"]),
		})
	}
	return selections
}

func stringAttr(value attr.Value) string {
	text, _ := value.(types.String)
	return text.ValueString()
}

func int64Attr(value attr.Value) int64 {
	number, _ := value.(types.Int64)
	return number.ValueInt64()
}
