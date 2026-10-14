package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*businessBlueprintsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*businessBlueprintsDataSource)(nil)
)

type businessBlueprintsDataSource struct{ client *Client }

type businessBlueprintsModel struct {
	ID         types.String         `tfsdk:"id"`
	IDPrefix   types.String         `tfsdk:"id_prefix"`
	Blueprints []blueprintItemModel `tfsdk:"blueprints"`
}

type blueprintItemModel struct {
	BlueprintID   types.String `tfsdk:"blueprint_id"`
	Version       types.Int64  `tfsdk:"version"`
	ContentDigest types.String `tfsdk:"content_digest"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
}

func NewBusinessBlueprintsDataSource() datasource.DataSource {
	return &businessBlueprintsDataSource{}
}

func (d *businessBlueprintsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_business_blueprints"
}

func (d *businessBlueprintsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the immutable blueprint versions available to the workspace, including compliance framework packs.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true},
			"id_prefix": schema.StringAttribute{Optional: true, Description: "Return only blueprints whose ID starts with this prefix, for example cerebro-framework-soc2."},
			"blueprints": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"blueprint_id":   schema.StringAttribute{Computed: true},
					"version":        schema.Int64Attribute{Computed: true},
					"content_digest": schema.StringAttribute{Computed: true},
					"name":           schema.StringAttribute{Computed: true},
					"description":    schema.StringAttribute{Computed: true},
				}},
			},
		},
	}
}

func (d *businessBlueprintsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *businessBlueprintsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config businessBlueprintsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	blueprints, err := d.client.listBlueprints(ctx, config.IDPrefix.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("List Deixic business blueprints", err.Error())
		return
	}
	config.Blueprints = []blueprintItemModel{}
	for _, blueprint := range blueprints {
		config.Blueprints = append(config.Blueprints, blueprintItemModel{
			BlueprintID:   types.StringValue(blueprint.GetBlueprintId()),
			Version:       types.Int64Value(blueprint.GetVersion()),
			ContentDigest: types.StringValue(blueprint.GetContentDigest()),
			Name:          types.StringValue(blueprint.GetName()),
			Description:   types.StringValue(blueprint.GetDescription()),
		})
	}
	config.ID = types.StringValue("business_blueprints")
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
