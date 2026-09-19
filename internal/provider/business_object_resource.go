package provider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	consolev1 "buf.build/gen/go/evalops-infra/proto/protocolbuffers/go/console/v1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"
)

var (
	_ resource.Resource                = (*businessObjectResource)(nil)
	_ resource.ResourceWithConfigure   = (*businessObjectResource)(nil)
	_ resource.ResourceWithImportState = (*businessObjectResource)(nil)
)

type businessObjectResource struct {
	client *Client
}

type businessObjectModel struct {
	ID             types.String `tfsdk:"id"`
	APIEndpoint    types.String `tfsdk:"api_endpoint"`
	OrganizationID types.String `tfsdk:"organization_id"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	TypeID         types.String `tfsdk:"type_id"`
	SchemaRevision types.Int64  `tfsdk:"schema_revision"`
	Revision       types.Int64  `tfsdk:"revision"`
	Values         types.Map    `tfsdk:"values"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

type fieldValueModel struct {
	Text              types.String `tfsdk:"text"`
	Integer           types.Int64  `tfsdk:"integer"`
	Boolean           types.Bool   `tfsdk:"boolean"`
	Decimal           types.String `tfsdk:"decimal"`
	MoneyAmount       types.String `tfsdk:"money_amount"`
	MoneyCurrency     types.String `tfsdk:"money_currency"`
	EnumValue         types.String `tfsdk:"enum_value"`
	Date              types.String `tfsdk:"date"`
	Timestamp         types.String `tfsdk:"timestamp"`
	ReferenceObjectID types.String `tfsdk:"reference_object_id"`
	ArtifactVersionID types.String `tfsdk:"artifact_version_id"`
	TextList          types.List   `tfsdk:"text_list"`
}

var fieldValueAttributeTypes = map[string]attr.Type{
	"text":                types.StringType,
	"integer":             types.Int64Type,
	"boolean":             types.BoolType,
	"decimal":             types.StringType,
	"money_amount":        types.StringType,
	"money_currency":      types.StringType,
	"enum_value":          types.StringType,
	"date":                types.StringType,
	"timestamp":           types.StringType,
	"reference_object_id": types.StringType,
	"artifact_version_id": types.StringType,
	"text_list":           types.ListType{ElemType: types.StringType},
}

func NewBusinessObjectResource() resource.Resource { return &businessObjectResource{} }

func (r *businessObjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_business_object"
}

func (r *businessObjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A tenant-scoped, durable Deixic business object backed by the canonical DeixicService CRUD facade.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Server-assigned stable business object ID.",
			},
			"api_endpoint": schema.StringAttribute{
				Computed:    true,
				Description: "Normalized API endpoint that owns this object's Terraform state.",
			},
			"organization_id": schema.StringAttribute{
				Computed:    true,
				Description: "Organization scope that owns this object's Terraform state.",
			},
			"workspace_id": schema.StringAttribute{
				Computed:    true,
				Description: "Workspace scope that owns this object's Terraform state.",
			},
			"type_id": schema.StringAttribute{
				Required:    true,
				Description: "ID of an existing Deixic business object type.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema_revision": schema.Int64Attribute{
				Required:    true,
				Description: "Exact published type schema revision. Updates may move this value forward.",
			},
			"revision": schema.Int64Attribute{
				Computed:    true,
				Description: "Owner-assigned optimistic concurrency revision.",
			},
			"values": schema.MapNestedAttribute{
				Required:    true,
				Description: "Business field values keyed by field ID. Each entry must set exactly one value kind; money requires both money_amount and money_currency.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"text":                schema.StringAttribute{Optional: true},
					"integer":             schema.Int64Attribute{Optional: true},
					"boolean":             schema.BoolAttribute{Optional: true},
					"decimal":             schema.StringAttribute{Optional: true, Description: "Exact canonical decimal text."},
					"money_amount":        schema.StringAttribute{Optional: true, Description: "Exact canonical decimal amount."},
					"money_currency":      schema.StringAttribute{Optional: true, Description: "ISO-style uppercase currency code."},
					"enum_value":          schema.StringAttribute{Optional: true},
					"date":                schema.StringAttribute{Optional: true, Description: "Calendar date accepted by the owner schema."},
					"timestamp":           schema.StringAttribute{Optional: true, Description: "Timestamp accepted by the owner schema."},
					"reference_object_id": schema.StringAttribute{Optional: true},
					"artifact_version_id": schema.StringAttribute{Optional: true},
					"text_list":           schema.ListAttribute{Optional: true, ElementType: types.StringType},
				}},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Owner-recorded creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Owner-recorded last update timestamp.",
			},
		},
	}
}

func (r *businessObjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *provider.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *businessObjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan businessObjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	values := valuesToProto(ctx, plan.Values, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		resp.Diagnostics.AddError("Create business object", err.Error())
		return
	}
	request := &consolev1.CreateBusinessObjectRequest{
		OrganizationId: r.client.OrganizationID(),
		WorkspaceId:    r.client.WorkspaceID(),
		IdempotencyKey: idempotencyKey,
		TypeId:         plan.TypeID.ValueString(),
		SchemaRevision: plan.SchemaRevision.ValueInt64(),
		Values:         values,
	}
	response := &consolev1.CreateBusinessObjectResponse{}
	if err := r.client.Invoke(ctx, "CreateBusinessObject", request, response); err != nil {
		resp.Diagnostics.AddError("Create Deixic business object", err.Error())
		return
	}
	object := response.GetObject()
	if err := validateCreateResponse(request, object); err != nil {
		resp.Diagnostics.AddError("Create Deixic business object", err.Error())
		return
	}
	state := objectToModel(ctx, object, r.client, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *businessObjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state businessObjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request := &consolev1.GetBusinessObjectRequest{
		OrganizationId: r.client.OrganizationID(),
		WorkspaceId:    r.client.WorkspaceID(),
		ObjectId:       state.ID.ValueString(),
	}
	response := &consolev1.GetBusinessObjectResponse{}
	if err := r.invokeForState(ctx, state, "GetBusinessObject", request, response); err != nil {
		if errors.Is(err, ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read Deixic business object", err.Error())
		return
	}
	object := response.GetObject()
	if err := validateReadResponse(state, request, object); err != nil {
		resp.Diagnostics.AddError("Read Deixic business object", err.Error())
		return
	}
	if object.GetDeleted() {
		resp.State.RemoveResource(ctx)
		return
	}
	refreshed := objectToModel(ctx, object, r.client, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *businessObjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan businessObjectModel
	var state businessObjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateStateBinding(state, r.client); err != nil {
		resp.Diagnostics.AddError("Update Deixic business object", err.Error())
		return
	}
	values := valuesToProto(ctx, plan.Values, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		resp.Diagnostics.AddError("Update business object", err.Error())
		return
	}
	request := &consolev1.UpdateBusinessObjectRequest{
		OrganizationId:   r.client.OrganizationID(),
		WorkspaceId:      r.client.WorkspaceID(),
		IdempotencyKey:   idempotencyKey,
		ObjectId:         state.ID.ValueString(),
		ExpectedRevision: state.Revision.ValueInt64(),
		Values:           values,
		SchemaRevision:   plan.SchemaRevision.ValueInt64(),
	}
	response := &consolev1.UpdateBusinessObjectResponse{}
	if err := r.invokeForState(ctx, state, "UpdateBusinessObject", request, response); err != nil {
		resp.Diagnostics.AddError("Update Deixic business object", err.Error())
		return
	}
	object := response.GetObject()
	if err := validateUpdateResponse(state, request, object); err != nil {
		resp.Diagnostics.AddError("Update Deixic business object", err.Error())
		return
	}
	updated := objectToModel(ctx, object, r.client, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *businessObjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state businessObjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateStateBinding(state, r.client); err != nil {
		resp.Diagnostics.AddError("Delete Deixic business object", err.Error())
		return
	}
	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		resp.Diagnostics.AddError("Delete business object", err.Error())
		return
	}
	request := &consolev1.DeleteBusinessObjectRequest{
		OrganizationId:   r.client.OrganizationID(),
		WorkspaceId:      r.client.WorkspaceID(),
		IdempotencyKey:   idempotencyKey,
		ObjectId:         state.ID.ValueString(),
		ExpectedRevision: state.Revision.ValueInt64(),
	}
	response := &consolev1.DeleteBusinessObjectResponse{}
	if err := r.invokeForState(ctx, state, "DeleteBusinessObject", request, response); err != nil {
		if errors.Is(err, ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Delete Deixic business object", err.Error())
		return
	}
	if err := validateDeleteResponse(request, response.GetObject()); err != nil {
		resp.Diagnostics.AddError("Delete Deixic business object", err.Error())
	}
}

func (r *businessObjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	organizationID, workspaceID, objectID, err := decodeBusinessObjectImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid business object import ID", err.Error())
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Import Deixic business object", "The provider is not configured.")
		return
	}
	if organizationID != r.client.OrganizationID() || workspaceID != r.client.WorkspaceID() {
		resp.Diagnostics.AddError(
			"Import scope does not match provider scope",
			"The import ID organization and workspace must match the configured Deixic provider.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), objectID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("api_endpoint"), r.client.Endpoint())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceID)...)
}

func valuesToProto(ctx context.Context, values types.Map, diagnostics *diag.Diagnostics) []*consolev1.BusinessFieldValue {
	if values.IsNull() || values.IsUnknown() {
		diagnostics.AddError("Invalid business object values", "values must be known and non-null.")
		return nil
	}
	models := map[string]fieldValueModel{}
	diagnostics.Append(values.ElementsAs(ctx, &models, false)...)
	if diagnostics.HasError() {
		return nil
	}
	fieldIDs := make([]string, 0, len(models))
	for fieldID := range models {
		fieldIDs = append(fieldIDs, fieldID)
	}
	sort.Strings(fieldIDs)
	result := make([]*consolev1.BusinessFieldValue, 0, len(fieldIDs))
	for _, fieldID := range fieldIDs {
		value, err := fieldValueToProto(ctx, fieldID, models[fieldID])
		if err != nil {
			diagnostics.AddAttributeError(path.Root("values").AtMapKey(fieldID), "Invalid business field value", err.Error())
			continue
		}
		result = append(result, value)
	}
	return result
}

func fieldValueToProto(ctx context.Context, fieldID string, model fieldValueModel) (*consolev1.BusinessFieldValue, error) {
	if strings.TrimSpace(fieldID) == "" {
		return nil, errors.New("field ID must not be empty or whitespace")
	}
	value := &consolev1.BusinessFieldValue{FieldId: fieldID}
	kinds := 0
	set := func(present bool, apply func()) {
		if present {
			kinds++
			apply()
		}
	}
	set(known(model.Text), func() { value.Value = &consolev1.BusinessFieldValue_Text{Text: model.Text.ValueString()} })
	set(known(model.Integer), func() { value.Value = &consolev1.BusinessFieldValue_Integer{Integer: model.Integer.ValueInt64()} })
	set(known(model.Boolean), func() { value.Value = &consolev1.BusinessFieldValue_Boolean{Boolean: model.Boolean.ValueBool()} })
	set(known(model.Decimal), func() { value.Value = &consolev1.BusinessFieldValue_Decimal{Decimal: model.Decimal.ValueString()} })
	moneyAmount := known(model.MoneyAmount)
	moneyCurrency := known(model.MoneyCurrency)
	if moneyAmount != moneyCurrency {
		return nil, errors.New("money_amount and money_currency must be set together")
	}
	set(moneyAmount, func() {
		value.Value = &consolev1.BusinessFieldValue_Money{Money: &consolev1.BusinessMoney{
			Amount: model.MoneyAmount.ValueString(), Currency: model.MoneyCurrency.ValueString(),
		}}
	})
	set(known(model.EnumValue), func() {
		value.Value = &consolev1.BusinessFieldValue_EnumValue{EnumValue: model.EnumValue.ValueString()}
	})
	set(known(model.Date), func() { value.Value = &consolev1.BusinessFieldValue_Date{Date: model.Date.ValueString()} })
	set(known(model.Timestamp), func() {
		value.Value = &consolev1.BusinessFieldValue_Timestamp{Timestamp: model.Timestamp.ValueString()}
	})
	set(known(model.ReferenceObjectID), func() {
		value.Value = &consolev1.BusinessFieldValue_Reference{Reference: &consolev1.BusinessObjectReference{ObjectId: model.ReferenceObjectID.ValueString()}}
	})
	set(known(model.ArtifactVersionID), func() {
		value.Value = &consolev1.BusinessFieldValue_Artifact{Artifact: &consolev1.BusinessArtifactReference{ArtifactVersionId: model.ArtifactVersionID.ValueString()}}
	})
	if !model.TextList.IsNull() && !model.TextList.IsUnknown() {
		var entries []string
		diagnostics := model.TextList.ElementsAs(ctx, &entries, false)
		if diagnostics.HasError() {
			return nil, fmt.Errorf("text_list must contain only known strings: %s", diagnostics.Errors()[0].Detail())
		}
		set(true, func() {
			value.Value = &consolev1.BusinessFieldValue_TextList{TextList: &consolev1.BusinessTextList{Values: entries}}
		})
	}
	if kinds != 1 {
		return nil, fmt.Errorf("set exactly one value kind; found %d", kinds)
	}
	return value, nil
}

type nullable interface {
	IsNull() bool
	IsUnknown() bool
}

func known(value nullable) bool { return !value.IsNull() && !value.IsUnknown() }

func objectToModel(ctx context.Context, object *consolev1.BusinessObject, client *Client, diagnostics *diag.Diagnostics) businessObjectModel {
	values := make(map[string]fieldValueModel, len(object.GetValues()))
	for _, value := range object.GetValues() {
		if value == nil {
			continue
		}
		model, err := fieldValueFromProto(ctx, value)
		if err != nil {
			diagnostics.AddError("Decode Deixic business object", err.Error())
			continue
		}
		if _, exists := values[value.GetFieldId()]; exists {
			diagnostics.AddError("Decode Deixic business object", fmt.Sprintf("Owner returned duplicate field ID %q.", value.GetFieldId()))
			continue
		}
		values[value.GetFieldId()] = model
	}
	terraformValues, mapDiagnostics := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: fieldValueAttributeTypes}, values)
	diagnostics.Append(mapDiagnostics...)
	return businessObjectModel{
		ID:             types.StringValue(object.GetObjectId()),
		APIEndpoint:    types.StringValue(client.Endpoint()),
		OrganizationID: types.StringValue(client.OrganizationID()),
		WorkspaceID:    types.StringValue(client.WorkspaceID()),
		TypeID:         types.StringValue(object.GetTypeId()),
		SchemaRevision: types.Int64Value(object.GetSchemaRevision()),
		Revision:       types.Int64Value(object.GetRevision()),
		Values:         terraformValues,
		CreatedAt:      types.StringValue(object.GetCreatedAt()),
		UpdatedAt:      types.StringValue(object.GetUpdatedAt()),
	}
}

func (r *businessObjectResource) invokeForState(
	ctx context.Context,
	state businessObjectModel,
	method string,
	request proto.Message,
	response proto.Message,
) error {
	if err := validateStateBinding(state, r.client); err != nil {
		return err
	}
	return r.client.Invoke(ctx, method, request, response)
}

func validateStateBinding(state businessObjectModel, client *Client) error {
	if client == nil {
		return errors.New("the provider is not configured")
	}
	bindings := []struct {
		name  string
		state types.String
		want  string
	}{
		{name: "API endpoint", state: state.APIEndpoint, want: client.Endpoint()},
		{name: "organization", state: state.OrganizationID, want: client.OrganizationID()},
		{name: "workspace", state: state.WorkspaceID, want: client.WorkspaceID()},
	}
	for _, binding := range bindings {
		if binding.state.IsNull() || binding.state.IsUnknown() || binding.state.ValueString() == "" {
			return fmt.Errorf("resource state is missing its original %s binding", binding.name)
		}
		if binding.state.ValueString() != binding.want {
			return fmt.Errorf(
				"resource state belongs to %s %q, but the provider is configured for %q; use the original provider configuration",
				binding.name,
				binding.state.ValueString(),
				binding.want,
			)
		}
	}
	return nil
}

func validateCreateResponse(request *consolev1.CreateBusinessObjectRequest, object *consolev1.BusinessObject) error {
	if err := validateAcceptedObject(object); err != nil {
		return err
	}
	if object.GetDeleted() {
		return errors.New("the owner returned a deleted object for create")
	}
	if object.GetTypeId() != request.GetTypeId() {
		return fmt.Errorf("the owner returned type ID %q for requested type %q", object.GetTypeId(), request.GetTypeId())
	}
	if object.GetSchemaRevision() != request.GetSchemaRevision() {
		return fmt.Errorf("the owner returned schema revision %d for requested revision %d", object.GetSchemaRevision(), request.GetSchemaRevision())
	}
	return nil
}

func validateReadResponse(state businessObjectModel, request *consolev1.GetBusinessObjectRequest, object *consolev1.BusinessObject) error {
	if err := validateAcceptedObject(object); err != nil {
		return err
	}
	if object.GetObjectId() != request.GetObjectId() {
		return fmt.Errorf("the owner returned object ID %q for requested object %q", object.GetObjectId(), request.GetObjectId())
	}
	if !state.TypeID.IsNull() && !state.TypeID.IsUnknown() && state.TypeID.ValueString() != "" && object.GetTypeId() != state.TypeID.ValueString() {
		return fmt.Errorf("the owner changed immutable type ID from %q to %q", state.TypeID.ValueString(), object.GetTypeId())
	}
	return nil
}

func validateUpdateResponse(state businessObjectModel, request *consolev1.UpdateBusinessObjectRequest, object *consolev1.BusinessObject) error {
	if err := validateAcceptedObject(object); err != nil {
		return err
	}
	if object.GetDeleted() {
		return errors.New("the owner returned a deleted object for update")
	}
	if object.GetObjectId() != request.GetObjectId() {
		return fmt.Errorf("the owner returned object ID %q for requested object %q", object.GetObjectId(), request.GetObjectId())
	}
	if !state.TypeID.IsNull() && !state.TypeID.IsUnknown() && object.GetTypeId() != state.TypeID.ValueString() {
		return fmt.Errorf("the owner changed immutable type ID from %q to %q", state.TypeID.ValueString(), object.GetTypeId())
	}
	if object.GetSchemaRevision() != request.GetSchemaRevision() {
		return fmt.Errorf("the owner returned schema revision %d for requested revision %d", object.GetSchemaRevision(), request.GetSchemaRevision())
	}
	if object.GetRevision() <= request.GetExpectedRevision() {
		return fmt.Errorf("the owner did not advance revision %d after update", request.GetExpectedRevision())
	}
	return nil
}

func validateDeleteResponse(request *consolev1.DeleteBusinessObjectRequest, object *consolev1.BusinessObject) error {
	if err := validateAcceptedObject(object); err != nil {
		return err
	}
	if object.GetObjectId() != request.GetObjectId() {
		return fmt.Errorf("the owner returned object ID %q for requested object %q", object.GetObjectId(), request.GetObjectId())
	}
	if !object.GetDeleted() {
		return fmt.Errorf("the owner did not confirm deletion for object %q", request.GetObjectId())
	}
	if object.GetRevision() <= request.GetExpectedRevision() {
		return fmt.Errorf("the owner did not advance revision %d after deletion", request.GetExpectedRevision())
	}
	return nil
}

func validateAcceptedObject(object *consolev1.BusinessObject) error {
	if object == nil {
		return errors.New("the owner returned no business object")
	}
	if object.GetObjectId() == "" {
		return errors.New("the owner returned a business object without an ID")
	}
	if object.GetTypeId() == "" {
		return fmt.Errorf("the owner returned business object %q without a type ID", object.GetObjectId())
	}
	if object.GetSchemaRevision() <= 0 {
		return fmt.Errorf("the owner returned business object %q without a positive schema revision", object.GetObjectId())
	}
	if object.GetRevision() <= 0 {
		return fmt.Errorf("the owner returned business object %q without a positive owner revision", object.GetObjectId())
	}
	return nil
}

func fieldValueFromProto(ctx context.Context, value *consolev1.BusinessFieldValue) (fieldValueModel, error) {
	model := emptyFieldValueModel()
	switch typed := value.GetValue().(type) {
	case *consolev1.BusinessFieldValue_Text:
		model.Text = types.StringValue(typed.Text)
	case *consolev1.BusinessFieldValue_Integer:
		model.Integer = types.Int64Value(typed.Integer)
	case *consolev1.BusinessFieldValue_Boolean:
		model.Boolean = types.BoolValue(typed.Boolean)
	case *consolev1.BusinessFieldValue_Decimal:
		model.Decimal = types.StringValue(typed.Decimal)
	case *consolev1.BusinessFieldValue_Money:
		if typed.Money == nil {
			return model, fmt.Errorf("field %q has an empty money value", value.GetFieldId())
		}
		model.MoneyAmount = types.StringValue(typed.Money.GetAmount())
		model.MoneyCurrency = types.StringValue(typed.Money.GetCurrency())
	case *consolev1.BusinessFieldValue_EnumValue:
		model.EnumValue = types.StringValue(typed.EnumValue)
	case *consolev1.BusinessFieldValue_Date:
		model.Date = types.StringValue(typed.Date)
	case *consolev1.BusinessFieldValue_Timestamp:
		model.Timestamp = types.StringValue(typed.Timestamp)
	case *consolev1.BusinessFieldValue_Reference:
		if typed.Reference == nil {
			return model, fmt.Errorf("field %q has an empty reference value", value.GetFieldId())
		}
		model.ReferenceObjectID = types.StringValue(typed.Reference.GetObjectId())
	case *consolev1.BusinessFieldValue_Artifact:
		if typed.Artifact == nil {
			return model, fmt.Errorf("field %q has an empty artifact value", value.GetFieldId())
		}
		model.ArtifactVersionID = types.StringValue(typed.Artifact.GetArtifactVersionId())
	case *consolev1.BusinessFieldValue_TextList:
		if typed.TextList == nil {
			return model, fmt.Errorf("field %q has an empty text-list value", value.GetFieldId())
		}
		list, diagnostics := types.ListValueFrom(ctx, types.StringType, typed.TextList.GetValues())
		if diagnostics.HasError() {
			return model, fmt.Errorf("field %q has an invalid text-list value: %s", value.GetFieldId(), diagnostics.Errors()[0].Detail())
		}
		model.TextList = list
	default:
		return model, fmt.Errorf("field %q has no supported value", value.GetFieldId())
	}
	return model, nil
}

func emptyFieldValueModel() fieldValueModel {
	return fieldValueModel{
		Text:              types.StringNull(),
		Integer:           types.Int64Null(),
		Boolean:           types.BoolNull(),
		Decimal:           types.StringNull(),
		MoneyAmount:       types.StringNull(),
		MoneyCurrency:     types.StringNull(),
		EnumValue:         types.StringNull(),
		Date:              types.StringNull(),
		Timestamp:         types.StringNull(),
		ReferenceObjectID: types.StringNull(),
		ArtifactVersionID: types.StringNull(),
		TextList:          types.ListNull(types.StringType),
	}
}

func newIdempotencyKey() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return "terraform-" + hex.EncodeToString(bytes), nil
}

func encodeBusinessObjectImportID(organizationID, workspaceID, objectID string) string {
	encode := base64.RawURLEncoding.EncodeToString
	return "v1." + encode([]byte(organizationID)) + "." + encode([]byte(workspaceID)) + "." + encode([]byte(objectID))
}

func decodeBusinessObjectImportID(importID string) (string, string, string, error) {
	parts := strings.Split(importID, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", "", "", errors.New("expected v1.<base64url-organization>.<base64url-workspace>.<base64url-object>")
	}
	decoded := make([]string, 3)
	for index, part := range parts[1:] {
		value, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || len(value) == 0 || !utf8.Valid(value) {
			return "", "", "", errors.New("import ID contains an invalid or empty base64url component")
		}
		decoded[index] = string(value)
	}
	return decoded[0], decoded[1], decoded[2], nil
}
