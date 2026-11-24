package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

var objectAs = basetypes.ObjectAsOptions{}

var (
	_ resource.Resource              = (*kubeconfigResource)(nil)
	_ resource.ResourceWithConfigure = (*kubeconfigResource)(nil)
)

// NewKubeconfigResource is the peernode_kubernetes_kubeconfig resource.
func NewKubeconfigResource() resource.Resource { return &kubeconfigResource{} }

type kubeconfigResource struct{ c *client.Client }

type kubeconfigModel struct {
	ID         types.String `tfsdk:"id"`
	ClusterID  types.String `tfsdk:"cluster_id"`
	Keepers    types.Map    `tfsdk:"keepers"`
	Kubeconfig types.String `tfsdk:"kubeconfig"`
	Filename   types.String `tfsdk:"filename"`
}

func (r *kubeconfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_kubeconfig"
}

func (r *kubeconfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An admin kubeconfig for a cluster. Every issue creates a new token and **revokes the " +
			"previous one** (also one downloaded from the dashboard), so keep one of these per cluster. Change " +
			"`keepers` to rotate the token. Destroying the resource only forgets the kubeconfig; the token stays " +
			"valid until the next one is issued or the cluster is deleted.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Same as `cluster_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"cluster_id": schema.StringAttribute{Required: true, MarkdownDescription: "Cluster ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"keepers": schema.MapAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Arbitrary values; changing any of them issues a new kubeconfig (token rotation).",
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()}},
			"kubeconfig": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "The kubeconfig (YAML).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"filename": schema.StringAttribute{Computed: true, MarkdownDescription: "Suggested file name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *kubeconfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.c = c
}

func (r *kubeconfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan kubeconfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kc, err := r.c.IssueKubeconfig(ctx, plan.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Issuing the kubeconfig failed", err.Error())
		return
	}
	plan.ID = plan.ClusterID
	plan.Kubeconfig = types.StringValue(kc.Kubeconfig)
	plan.Filename = types.StringValue(kc.Filename)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kubeconfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state kubeconfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cl, err := r.c.GetCluster(ctx, state.ClusterID.ValueString())
	if client.IsNotFound(err) || (err == nil && (cl.Status == client.StatusDeleting || !cl.HasKubeconfig)) {
		// The cluster is gone or its token was revoked: issue a new kubeconfig on the next apply.
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the cluster failed", err.Error())
	}
}

func (r *kubeconfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every attribute either forces replacement or is computed; nothing to do.
	var plan kubeconfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *kubeconfigResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// The API has no way to revoke a kubeconfig without issuing a new one; forgetting it is all we can do.
}
