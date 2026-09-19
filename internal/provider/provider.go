package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const providerTypeName = "deixic"

var _ provider.Provider = (*deixicProvider)(nil)

type deixicProvider struct {
	version    string
	httpClient *http.Client
}

type providerModel struct {
	Endpoint       types.String `tfsdk:"endpoint"`
	Token          types.String `tfsdk:"token"`
	OrganizationID types.String `tfsdk:"organization_id"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
}

// New returns the protocol-v6 Deixic Terraform provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &deixicProvider{version: version}
	}
}

func (p *deixicProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = providerTypeName
	resp.Version = p.version
}

func (p *deixicProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Description: "Manage Deixic resources through the tenant-scoped binary Connect API.",
		Attributes: map[string]providerschema.Attribute{
			"endpoint": providerschema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the Deixic Platform API. May also be set with DEIXIC_ENDPOINT.",
			},
			"token": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Bearer token with console:read and console:write access. May also be set with DEIXIC_TOKEN.",
			},
			"organization_id": providerschema.StringAttribute{
				Optional:    true,
				Description: "Deixic organization ID. May also be set with DEIXIC_ORGANIZATION_ID.",
			},
			"workspace_id": providerschema.StringAttribute{
				Optional:    true,
				Description: "Deixic workspace ID. May also be set with DEIXIC_WORKSPACE_ID.",
			},
		},
	}
}

func (p *deixicProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := configuredString(config.Endpoint, "endpoint", "DEIXIC_ENDPOINT", &resp.Diagnostics)
	token := configuredString(config.Token, "token", "DEIXIC_TOKEN", &resp.Diagnostics)
	organizationID := configuredString(config.OrganizationID, "organization_id", "DEIXIC_ORGANIZATION_ID", &resp.Diagnostics)
	workspaceID := configuredString(config.WorkspaceID, "workspace_id", "DEIXIC_WORKSPACE_ID", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	requireProviderValue(&resp.Diagnostics, "endpoint", "DEIXIC_ENDPOINT", endpoint)
	requireProviderValue(&resp.Diagnostics, "token", "DEIXIC_TOKEN", token)
	requireProviderValue(&resp.Diagnostics, "organization_id", "DEIXIC_ORGANIZATION_ID", organizationID)
	requireProviderValue(&resp.Diagnostics, "workspace_id", "DEIXIC_WORKSPACE_ID", workspaceID)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := NewClient(endpoint, token, organizationID, workspaceID, p.httpClient)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Deixic provider configuration", err.Error())
		return
	}
	resp.ResourceData = client
}

func (p *deixicProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewBusinessObjectResource}
}

func (p *deixicProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func configuredString(value types.String, attribute, environment string, diagnostics *diag.Diagnostics) string {
	if value.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root(attribute),
			"Unknown Deixic provider configuration",
			fmt.Sprintf("The %q provider attribute must be known; environment fallback is not used for an unknown value.", attribute),
		)
		return ""
	}
	if !value.IsNull() {
		return value.ValueString()
	}
	return os.Getenv(environment)
}

func requireProviderValue(diagnostics *diag.Diagnostics, attribute, environment, value string) {
	if value == "" {
		diagnostics.AddError(
			"Missing Deixic provider configuration",
			fmt.Sprintf("Set the %q provider attribute or the %s environment variable.", attribute, environment),
		)
	}
}
