package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

var (
	_ resource.Resource              = (*accessKeyResource)(nil)
	_ resource.ResourceWithConfigure = (*accessKeyResource)(nil)
)

// NewAccessKeyResource is the peernode_storage_access_key resource.
func NewAccessKeyResource() resource.Resource { return &accessKeyResource{} }

type accessKeyResource struct{ c *client.Client }

type accessKeyModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Bucket          types.String `tfsdk:"bucket"`
	ReadOnly        types.Bool   `tfsdk:"read_only"`
	Keepers         types.Map    `tfsdk:"keepers"`
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	Endpoint        types.String `tfsdk:"endpoint"`
	Region          types.String `tfsdk:"region"`
}

func (r *accessKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_access_key"
}

func (r *accessKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An S3 access key for `s3` buckets. The secret is available only when the key is created and is " +
			"stored in the Terraform state, so protect the state. Change `keepers` to rotate the key (a new key is created " +
			"and the old one revoked). Destroying the resource revokes the key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Key ID (not the access key ID).", PlanModifiers: keep},
			"name": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				MarkdownDescription: "A name to recognise the key.", PlanModifiers: replace},
			"bucket": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				MarkdownDescription: "Limit the key to this `s3` bucket. Empty: every `s3` bucket of the account.", PlanModifiers: replace},
			"read_only": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Allow only reads and listings.", PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()}},
			"keepers": schema.MapAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Arbitrary values; changing any of them replaces the key (rotation).",
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()}},
			"access_key_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The S3 access key ID (`AWS_ACCESS_KEY_ID`).", PlanModifiers: keep},
			"secret_access_key": schema.StringAttribute{Computed: true, Sensitive: true,
				MarkdownDescription: "The S3 secret access key (`AWS_SECRET_ACCESS_KEY`).", PlanModifiers: keep},
			"endpoint": schema.StringAttribute{Computed: true, MarkdownDescription: "S3 endpoint URL.", PlanModifiers: keep},
			"region":   schema.StringAttribute{Computed: true, MarkdownDescription: "Region to configure in S3 clients.", PlanModifiers: keep},
		},
	}
}

func (r *accessKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *accessKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.c.CreateAccessKey(ctx, client.CreateAccessKeyInput{Name: plan.Name.ValueString(), Bucket: plan.Bucket.ValueString(), ReadOnly: plan.ReadOnly.ValueBool()})
	if err != nil {
		resp.Diagnostics.AddError("Creating the access key failed", err.Error())
		return
	}
	plan.ID = types.StringValue(k.AccessKey.ID)
	plan.AccessKeyID = types.StringValue(k.AccessKey.AccessKeyID)
	plan.SecretAccessKey = types.StringValue(k.AccessKey.SecretAccessKey)
	plan.Endpoint = types.StringValue(k.Endpoint)
	plan.Region = types.StringValue(k.Region)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *accessKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.c.GetAccessKey(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx) // revoked elsewhere: create a new one on the next apply
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the access key failed", err.Error())
		return
	}
	state.AccessKeyID = types.StringValue(k.AccessKeyID)
	state.Name = types.StringValue(k.Name)
	state.Bucket = types.StringValue(k.Bucket)
	state.ReadOnly = types.BoolValue(k.ReadOnly)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *accessKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every argument forces replacement; nothing changes in place.
	var plan accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *accessKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteAccessKey(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Revoking the access key failed", err.Error())
	}
}
