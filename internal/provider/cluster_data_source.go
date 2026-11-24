package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

var (
	_ datasource.DataSource                     = (*clusterDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*clusterDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*clusterDataSource)(nil)
)

// NewClusterDataSource is the peernode_kubernetes_cluster data source.
func NewClusterDataSource() datasource.DataSource { return &clusterDataSource{} }

type clusterDataSource struct{ c *client.Client }

type clusterDataModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Mode        types.String `tfsdk:"mode"`
	Status      types.String `tfsdk:"status"`
	PublicHosts types.List   `tfsdk:"public_hosts"`
	CPUMilli    types.Int64  `tfsdk:"size_cpu_milli"`
	MemoryBytes types.Int64  `tfsdk:"size_memory_bytes"`
	DiskBytes   types.Int64  `tfsdk:"size_disk_bytes"`
	HourlyMicro types.Int64  `tfsdk:"hourly_micro"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (d *clusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster"
}

func (d *clusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an existing cluster of the account by `id` or `name`.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Cluster ID."},
			"name":              schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Cluster name."},
			"mode":              schema.StringAttribute{Computed: true, MarkdownDescription: "`autopilot`, `auto` or `pools`."},
			"status":            schema.StringAttribute{Computed: true, MarkdownDescription: "Cluster status."},
			"public_hosts":      schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Public HTTPS hostnames."},
			"size_cpu_milli":    schema.Int64Attribute{Computed: true, MarkdownDescription: "CPU, millicores."},
			"size_memory_bytes": schema.Int64Attribute{Computed: true, MarkdownDescription: "Memory, bytes."},
			"size_disk_bytes":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Volumes, bytes."},
			"hourly_micro":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Hourly price of the reserved size, micro-credits."},
			"created_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time."},
		},
	}
}

func (d *clusterDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *clusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	d.c = c
}

func (d *clusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m clusterDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var cl *client.Cluster
	if !m.ID.IsNull() {
		c, err := d.c.GetCluster(ctx, m.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Reading the cluster failed", err.Error())
			return
		}
		cl = c
	} else {
		all, err := d.c.ListClusters(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Listing clusters failed", err.Error())
			return
		}
		for i := range all {
			if all[i].Name == m.Name.ValueString() {
				cl = &all[i]
			}
		}
		if cl == nil {
			resp.Diagnostics.AddError("Cluster not found", fmt.Sprintf("no cluster named %q in this account", m.Name.ValueString()))
			return
		}
	}
	m.ID, m.Name, m.Mode, m.Status = types.StringValue(cl.ID), types.StringValue(cl.Name), types.StringValue(cl.Spec.Mode), types.StringValue(cl.Status)
	hosts := make([]attr.Value, 0, len(cl.PublicHosts))
	for _, h := range cl.PublicHosts {
		hosts = append(hosts, types.StringValue(h))
	}
	hv, diags := types.ListValue(types.StringType, hosts)
	resp.Diagnostics.Append(diags...)
	m.PublicHosts = hv
	m.CPUMilli, m.MemoryBytes, m.DiskBytes = types.Int64Value(cl.Size.CPUMilli), types.Int64Value(cl.Size.MemoryBytes), types.Int64Value(cl.Size.DiskBytes)
	m.HourlyMicro, m.CreatedAt = types.Int64Value(cl.HourlyMicro), types.StringValue(cl.CreatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
