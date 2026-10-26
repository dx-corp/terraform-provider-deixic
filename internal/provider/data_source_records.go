package provider

import (
	"context"
	"fmt"
	"strings"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*recordsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*recordsDataSource)(nil)
)

// recordsDataSource lists the key/name records of one compliance type.
type recordsDataSource struct {
	client       *Client
	suffix       string
	sourceTypeID string
	noun         string
}

type recordsModel struct {
	ID      types.String  `tfsdk:"id"`
	TypeID  types.String  `tfsdk:"type_id"`
	Key     types.String  `tfsdk:"key"`
	Search  types.String  `tfsdk:"search"`
	Records []recordModel `tfsdk:"records"`
}

type recordModel struct {
	ID   types.String `tfsdk:"id"`
	Key  types.String `tfsdk:"key"`
	Name types.String `tfsdk:"name"`
}

func NewFrameworksDataSource() datasource.DataSource {
	return &recordsDataSource{suffix: "_frameworks", sourceTypeID: "framework", noun: "framework"}
}

func NewControlsDataSource() datasource.DataSource {
	return &recordsDataSource{suffix: "_controls", sourceTypeID: "control", noun: "control"}
}

func (d *recordsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.suffix
}

func (d *recordsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: fmt.Sprintf("Lists the %s records of the workspace compliance library.", d.noun),
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true},
			"type_id": schema.StringAttribute{Optional: true, Description: fmt.Sprintf("Type ID of the %s records. Resolved from the installed library when omitted.", d.noun)},
			"key":     schema.StringAttribute{Optional: true, Description: "Return only the record with this exact key."},
			"search":  schema.StringAttribute{Optional: true, Description: "Return only records whose key or name contains this text, ignoring case."},
			"records": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":   schema.StringAttribute{Computed: true, Description: "Business object ID."},
					"key":  schema.StringAttribute{Computed: true},
					"name": schema.StringAttribute{Computed: true},
				}},
			},
		},
	}
}

func (d *recordsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *recordsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config recordsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	typeID, err := d.client.resolveTypeID(ctx, config.TypeID.ValueString(), d.sourceTypeID)
	if err != nil {
		resp.Diagnostics.AddError("Resolve "+d.noun+" type", err.Error())
		return
	}
	objects, err := d.client.listObjects(ctx, typeID)
	if err != nil {
		resp.Diagnostics.AddError("List Deixic "+d.noun+" records", err.Error())
		return
	}
	needle := strings.ToLower(config.Search.ValueString())
	records := []recordModel{}
	for _, object := range filterRecords(objects, config.Key.ValueString(), needle) {
		records = append(records, recordModel{
			ID:   types.StringValue(object.GetObjectId()),
			Key:  types.StringValue(fieldText(object, "key")),
			Name: types.StringValue(fieldText(object, "name")),
		})
	}
	config.ID = types.StringValue(typeID)
	config.TypeID = types.StringValue(typeID)
	config.Records = records
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func filterRecords(objects []*publicv1.BusinessObject, key, needle string) []*publicv1.BusinessObject {
	var kept []*publicv1.BusinessObject
	for _, object := range objects {
		if key != "" && fieldText(object, "key") != key {
			continue
		}
		haystack := strings.ToLower(fieldText(object, "key") + " " + fieldText(object, "name"))
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		kept = append(kept, object)
	}
	return kept
}
