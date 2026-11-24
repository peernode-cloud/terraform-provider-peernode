// Package provider implements the PeerNode Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

// DefaultEndpoint is the public PeerNode API.
const DefaultEndpoint = "https://peernode.cloud"

var _ provider.Provider = (*peernodeProvider)(nil)

type peernodeProvider struct {
	version string
}

// New returns the provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &peernodeProvider{version: version} }
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

func (p *peernodeProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "peernode"
	resp.Version = p.version
}

func (p *peernodeProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [PeerNode](https://peernode.cloud) resources: Kubernetes clusters and their kubeconfigs.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "PeerNode API endpoint. Defaults to `$PEERNODE_ENDPOINT`, then `" + DefaultEndpoint + "`.",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "API token (`pnk_...`, created under Settings → API tokens). Defaults to `$PEERNODE_TOKEN`.",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

func (p *peernodeProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint := os.Getenv("PEERNODE_ENDPOINT")
	if !cfg.Endpoint.IsNull() && !cfg.Endpoint.IsUnknown() {
		endpoint = cfg.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	token := os.Getenv("PEERNODE_TOKEN")
	if !cfg.Token.IsNull() && !cfg.Token.IsUnknown() {
		token = cfg.Token.ValueString()
	}
	if token == "" {
		resp.Diagnostics.AddError("Missing PeerNode API token",
			"Set the provider's `token` argument or the PEERNODE_TOKEN environment variable to an API token (Settings → API tokens).")
		return
	}
	c := client.New(endpoint, token, "terraform-provider-peernode/"+p.version)
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *peernodeProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewClusterResource, NewKubeconfigResource}
}

func (p *peernodeProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewClusterDataSource}
}
