package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

var (
	_ resource.Resource                = (*bucketResource)(nil)
	_ resource.ResourceWithConfigure   = (*bucketResource)(nil)
	_ resource.ResourceWithImportState = (*bucketResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*bucketResource)(nil)
)

// NewBucketResource is the peernode_storage_bucket resource.
func NewBucketResource() resource.Resource { return &bucketResource{} }

type bucketResource struct{ c *client.Client }

type bucketModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Kind         types.String `tfsdk:"kind"`
	Visibility   types.String `tfsdk:"visibility"`
	NodeCount    types.Int64  `tfsdk:"node_count"`
	ForceDestroy types.Bool   `tfsdk:"force_destroy"`
	Endpoint     types.String `tfsdk:"endpoint"`
	PublicURL    types.String `tfsdk:"public_url"`
	CreatedAt    types.String `tfsdk:"created_at"`
}

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket"
}

func (r *bucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A storage bucket. `s3` buckets (the default) are used with any S3 tool at `endpoint` " +
			"(path-style, region `us-east-1`) with keys from `peernode_storage_access_key`; their names are global. " +
			"`vault` buckets are end-to-end encrypted by the browser or the `peernode` CLI and have no S3 access.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Bucket ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "Bucket name: 3-63 lowercase letters, digits or hyphens.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(bucketNameRe, "must be 3-63 lowercase letters, digits or hyphens")}},
			"kind": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("s3"),
				MarkdownDescription: "`s3` (default) or `vault`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("s3", "vault")}},
			"visibility": schema.StringAttribute{Optional: true, Computed: true,
				MarkdownDescription: "`private` (default for `s3`: every request needs an access key) or `public` (anyone can download " +
					"objects by URL; listing and uploads still need a key). Changed in place. Not used by `vault` buckets.",
				Validators: []validator.String{stringvalidator.OneOf("private", "public")}},
			"node_count": schema.Int64Attribute{Optional: true, Computed: true,
				MarkdownDescription: "Storage nodes that hold each file; one of them may be lost. `s3`: 2-17 (default 2). `vault`: 1-17.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplaceIfConfigured(), int64planmodifier.UseStateForUnknown()},
				Validators:          []validator.Int64{int64validator.Between(1, 17)}},
			"force_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Delete every object when the bucket is destroyed. Without it, destroying a bucket that has objects fails."},
			"endpoint": schema.StringAttribute{Computed: true, MarkdownDescription: "S3 endpoint (`s3` buckets).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"public_url": schema.StringAttribute{Computed: true, MarkdownDescription: "URL prefix of the objects of a public `s3` bucket (empty otherwise)."},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan fills the kind-dependent defaults and refuses visibility on vault buckets.
func (r *bucketResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Kind.IsUnknown() {
		return
	}
	s3 := plan.Kind.ValueString() == "s3"
	if plan.Visibility.IsUnknown() || plan.Visibility.IsNull() {
		if s3 {
			plan.Visibility = types.StringValue("private")
		} else {
			plan.Visibility = types.StringNull()
		}
	} else if !s3 {
		resp.Diagnostics.AddAttributeError(path.Root("visibility"), "Not used by vault buckets", "Only s3 buckets have a visibility.")
		return
	}
	if plan.NodeCount.IsUnknown() && req.State.Raw.IsNull() {
		if s3 {
			plan.NodeCount = types.Int64Value(2)
		}
	}
	if s3 && !plan.NodeCount.IsUnknown() && !plan.NodeCount.IsNull() && plan.NodeCount.ValueInt64() < 2 {
		resp.Diagnostics.AddAttributeError(path.Root("node_count"), "Too few nodes", "s3 buckets keep every file on at least 2 storage nodes.")
		return
	}
	if !s3 {
		plan.PublicURL = types.StringValue("")
		plan.Endpoint = types.StringValue("")
	} else if !req.State.Raw.IsNull() {
		var state bucketModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if plan.Visibility.Equal(state.Visibility) {
			plan.PublicURL = state.PublicURL
		}
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (m *bucketModel) fill(b *client.Bucket) {
	m.ID = types.StringValue(b.ID)
	m.Name = types.StringValue(b.Name)
	m.Kind = types.StringValue(b.Kind)
	if b.Kind == "s3" {
		m.Visibility = types.StringValue(b.Visibility)
	} else {
		m.Visibility = types.StringNull()
	}
	m.NodeCount = types.Int64Value(b.Nodes())
	m.Endpoint = types.StringValue(b.Endpoint)
	m.PublicURL = types.StringValue(b.PublicURL)
	m.CreatedAt = types.StringValue(b.CreatedAt)
	if m.ForceDestroy.IsNull() || m.ForceDestroy.IsUnknown() {
		m.ForceDestroy = types.BoolValue(false)
	}
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.CreateBucketInput{Name: plan.Name.ValueString(), Kind: plan.Kind.ValueString()}
	if !plan.Visibility.IsNull() && !plan.Visibility.IsUnknown() {
		in.Visibility = plan.Visibility.ValueString()
	}
	if !plan.NodeCount.IsNull() && !plan.NodeCount.IsUnknown() {
		in.NodeCount = plan.NodeCount.ValueInt64()
	}
	b, err := r.c.CreateBucket(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Creating the bucket failed", err.Error())
		return
	}
	plan.fill(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, err := r.c.GetBucket(ctx, state.Name.ValueString())
	if client.IsNotFound(err) || (err == nil && !state.ID.IsNull() && state.ID.ValueString() != "" && b.ID != state.ID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the bucket failed", err.Error())
		return
	}
	state.fill(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, err := r.c.GetBucket(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading the bucket failed", err.Error())
		return
	}
	if plan.Kind.ValueString() == "s3" && !plan.Visibility.IsNull() && plan.Visibility.ValueString() != b.Visibility {
		if b, err = r.c.SetBucketVisibility(ctx, b.Name, plan.Visibility.ValueString()); err != nil {
			resp.Diagnostics.AddError("Changing the visibility failed", err.Error())
			return
		}
	}
	plan.fill(b)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	if state.ForceDestroy.ValueBool() {
		keys, err := r.c.ObjectKeys(ctx, name)
		if err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Listing the bucket's objects failed", err.Error())
			return
		}
		for _, k := range keys {
			if err := r.c.DeleteObject(ctx, name, k); err != nil {
				resp.Diagnostics.AddError("Deleting an object failed", fmt.Sprintf("%s: %s", k, err))
				return
			}
		}
	}
	if err := r.c.DeleteBucket(ctx, name); err != nil {
		resp.Diagnostics.AddError("Deleting the bucket failed", err.Error()+
			"\n\nA bucket must be empty to be deleted. Delete its objects, or set force_destroy = true.")
	}
}

// ImportState imports a bucket by name.
func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
