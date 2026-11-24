package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

// How long cluster operations may take and how often they are polled.
var (
	CreateTimeout = 20 * time.Minute
	DeleteTimeout = 15 * time.Minute
	PollInterval  = 10 * time.Second
)

var (
	_ resource.Resource                   = (*clusterResource)(nil)
	_ resource.ResourceWithConfigure      = (*clusterResource)(nil)
	_ resource.ResourceWithImportState    = (*clusterResource)(nil)
	_ resource.ResourceWithValidateConfig = (*clusterResource)(nil)
)

// NewClusterResource is the peernode_kubernetes_cluster resource.
func NewClusterResource() resource.Resource { return &clusterResource{} }

type clusterResource struct{ c *client.Client }

type clusterModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Mode        types.String `tfsdk:"mode"`
	Autopilot   types.Object `tfsdk:"autopilot"`
	Auto        types.Object `tfsdk:"auto"`
	Pools       types.List   `tfsdk:"pools"`
	Status      types.String `tfsdk:"status"`
	PublicHosts types.List   `tfsdk:"public_hosts"`
	CPUMilli    types.Int64  `tfsdk:"size_cpu_milli"`
	MemoryBytes types.Int64  `tfsdk:"size_memory_bytes"`
	DiskBytes   types.Int64  `tfsdk:"size_disk_bytes"`
	HourlyMicro types.Int64  `tfsdk:"hourly_micro"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

var (
	autopilotTypes = map[string]attr.Type{
		"max_cpu": types.Float64Type, "max_memory_gb": types.Float64Type, "max_disk_gb": types.Int64Type,
	}
	autoTypes = map[string]attr.Type{
		"cpu": types.Float64Type, "memory_gb": types.Float64Type, "disk_gb": types.Int64Type,
		"min_pod_cpu": types.Float64Type, "min_pod_memory_gb": types.Float64Type,
	}
	poolTypes = map[string]attr.Type{
		"name": types.StringType, "count": types.Int64Type, "cpu": types.Float64Type,
		"memory_gb": types.Float64Type, "disk_gb": types.Int64Type,
	}
)

func (r *clusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster"
}

func (r *clusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A managed Kubernetes cluster on PeerNode provider machines (one vcluster per cluster). " +
			"`autopilot` (default) pays for what the pods request, up to an optional cap; `auto` reserves a size and " +
			"places it where convenient; `pools` reserves `count × (cpu, memory, disk)` nodes. The mode and the name " +
			"cannot change in place; sizes can (resizing does not restart the cluster).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Cluster ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Cluster name (lowercase letters, digits and dashes). Changing it replaces the cluster.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"mode": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("autopilot"),
				MarkdownDescription: "`autopilot` (default), `auto` or `pools`. Changing it replaces the cluster.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("autopilot", "auto", "pools")},
			},
			"autopilot": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Autopilot mode only: a cap on what the cluster's pods may request together. " +
					"When omitted the platform default applies (shown after apply).",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"max_cpu":       schema.Float64Attribute{Required: true, MarkdownDescription: "CPU cores."},
					"max_memory_gb": schema.Float64Attribute{Required: true, MarkdownDescription: "Memory, GiB."},
					"max_disk_gb":   schema.Int64Attribute{Required: true, MarkdownDescription: "Persistent volumes, GiB."},
				},
			},
			"auto": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Auto mode only: the cluster size, reserved on provider machines and paid for by the hour.",
				Attributes: map[string]schema.Attribute{
					"cpu":       schema.Float64Attribute{Required: true, MarkdownDescription: "CPU cores (0.5-256)."},
					"memory_gb": schema.Float64Attribute{Required: true, MarkdownDescription: "Memory, GiB (0.5-1024)."},
					"disk_gb": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0),
						MarkdownDescription: "Persistent volumes, GiB."},
					"min_pod_cpu": schema.Float64Attribute{Optional: true,
						MarkdownDescription: "Guarantee one part of the reservation at least this big (CPU cores)."},
					"min_pod_memory_gb": schema.Float64Attribute{Optional: true,
						MarkdownDescription: "Guarantee one part of the reservation at least this big (memory, GiB)."},
				},
			},
			"pools": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Pools mode only: 1-5 node pools; the first is the default for pods that do not select one.",
				Validators:          []validator.List{listvalidator.SizeBetween(1, 5)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":      schema.StringAttribute{Required: true, MarkdownDescription: "Pool name (`nodeSelector: {peernode.cloud/pool: <name>}`)."},
						"count":     schema.Int64Attribute{Required: true, MarkdownDescription: "Nodes (1-10)."},
						"cpu":       schema.Float64Attribute{Required: true, MarkdownDescription: "CPU cores per node (0.25-64)."},
						"memory_gb": schema.Float64Attribute{Required: true, MarkdownDescription: "Memory per node, GiB (0.25-256)."},
						"disk_gb": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0),
							MarkdownDescription: "Persistent volumes per node, GiB."},
					},
				},
			},
			"status":            schema.StringAttribute{Computed: true, MarkdownDescription: "`provisioning`, `ready`, `failed` or `deleting`.", PlanModifiers: keep},
			"public_hosts":      schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Public HTTPS hostnames (set once the cluster has a LoadBalancer service).", PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()}},
			"size_cpu_milli":    schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{keepIfSpecUnchanged{}}, MarkdownDescription: "The most the pods may request: CPU, millicores."},
			"size_memory_bytes": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{keepIfSpecUnchanged{}}, MarkdownDescription: "The most the pods may request: memory, bytes."},
			"size_disk_bytes":   schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{keepIfSpecUnchanged{}}, MarkdownDescription: "Volumes, bytes."},
			"hourly_micro":      schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{keepIfSpecUnchanged{}}, MarkdownDescription: "Price of the reserved size per hour in micro-credits (0 for autopilot, which pays for use)."},
			"created_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *clusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *clusterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m clusterModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Mode.IsUnknown() {
		return
	}
	mode := m.Mode.ValueString()
	if m.Mode.IsNull() {
		mode = "autopilot"
	}
	set := func(v attr.Value) bool { return !v.IsNull() }
	bad := func(attr, why string) {
		resp.Diagnostics.AddAttributeError(path.Root(attr), "Invalid cluster spec", why)
	}
	switch mode {
	case "autopilot":
		if set(m.Auto) {
			bad("auto", "`auto` is only allowed with mode = \"auto\".")
		}
		if set(m.Pools) {
			bad("pools", "`pools` is only allowed with mode = \"pools\".")
		}
	case "auto":
		if !set(m.Auto) && !m.Auto.IsUnknown() {
			bad("auto", "mode = \"auto\" needs an `auto` block with the cluster size.")
		}
		if set(m.Autopilot) {
			bad("autopilot", "`autopilot` is only allowed with mode = \"autopilot\".")
		}
		if set(m.Pools) {
			bad("pools", "`pools` is only allowed with mode = \"pools\".")
		}
	case "pools":
		if !set(m.Pools) && !m.Pools.IsUnknown() {
			bad("pools", "mode = \"pools\" needs at least one pool.")
		}
		if set(m.Autopilot) {
			bad("autopilot", "`autopilot` is only allowed with mode = \"autopilot\".")
		}
		if set(m.Auto) {
			bad("auto", "`auto` is only allowed with mode = \"auto\".")
		}
	}
}

// spec builds the API spec from a plan.
func (m *clusterModel) spec(ctx context.Context) (client.Spec, diag.Diagnostics) {
	var diags diag.Diagnostics
	s := client.Spec{Mode: m.Mode.ValueString()}
	if s.Mode == "" {
		s.Mode = "autopilot"
	}
	if !m.Autopilot.IsNull() && !m.Autopilot.IsUnknown() {
		var a struct {
			MaxCPU      types.Float64 `tfsdk:"max_cpu"`
			MaxMemoryGB types.Float64 `tfsdk:"max_memory_gb"`
			MaxDiskGB   types.Int64   `tfsdk:"max_disk_gb"`
		}
		diags.Append(m.Autopilot.As(ctx, &a, objectAs)...)
		s.Autopilot = &client.AutopilotSpec{MaxCPU: a.MaxCPU.ValueFloat64(), MaxMemoryGB: a.MaxMemoryGB.ValueFloat64(), MaxDiskGB: a.MaxDiskGB.ValueInt64()}
	}
	if !m.Auto.IsNull() && !m.Auto.IsUnknown() {
		var a struct {
			CPU            types.Float64 `tfsdk:"cpu"`
			MemoryGB       types.Float64 `tfsdk:"memory_gb"`
			DiskGB         types.Int64   `tfsdk:"disk_gb"`
			MinPodCPU      types.Float64 `tfsdk:"min_pod_cpu"`
			MinPodMemoryGB types.Float64 `tfsdk:"min_pod_memory_gb"`
		}
		diags.Append(m.Auto.As(ctx, &a, objectAs)...)
		s.Auto = &client.AutoSpec{CPU: a.CPU.ValueFloat64(), MemoryGB: a.MemoryGB.ValueFloat64(), DiskGB: a.DiskGB.ValueInt64(),
			MinPodCPU: a.MinPodCPU.ValueFloat64(), MinPodMemoryGB: a.MinPodMemoryGB.ValueFloat64()}
	}
	if !m.Pools.IsNull() && !m.Pools.IsUnknown() {
		var pools []struct {
			Name     types.String  `tfsdk:"name"`
			Count    types.Int64   `tfsdk:"count"`
			CPU      types.Float64 `tfsdk:"cpu"`
			MemoryGB types.Float64 `tfsdk:"memory_gb"`
			DiskGB   types.Int64   `tfsdk:"disk_gb"`
		}
		diags.Append(m.Pools.ElementsAs(ctx, &pools, false)...)
		for _, p := range pools {
			s.Pools = append(s.Pools, client.PoolSpec{Name: p.Name.ValueString(), Count: p.Count.ValueInt64(),
				CPU: p.CPU.ValueFloat64(), MemoryGB: p.MemoryGB.ValueFloat64(), DiskGB: p.DiskGB.ValueInt64()})
		}
	}
	return s, diags
}

// fill copies a cluster from the API into the model.
func (m *clusterModel) fill(cl *client.Cluster) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(cl.ID)
	m.Name = types.StringValue(cl.Name)
	m.Mode = types.StringValue(cl.Spec.Mode)
	m.Autopilot = types.ObjectNull(autopilotTypes)
	if a := cl.Spec.Autopilot; a != nil {
		v, d := types.ObjectValue(autopilotTypes, map[string]attr.Value{
			"max_cpu": types.Float64Value(a.MaxCPU), "max_memory_gb": types.Float64Value(a.MaxMemoryGB), "max_disk_gb": types.Int64Value(a.MaxDiskGB),
		})
		diags.Append(d...)
		m.Autopilot = v
	}
	m.Auto = types.ObjectNull(autoTypes)
	if a := cl.Spec.Auto; a != nil {
		opt := func(f float64) types.Float64 {
			if f == 0 {
				return types.Float64Null()
			}
			return types.Float64Value(f)
		}
		v, d := types.ObjectValue(autoTypes, map[string]attr.Value{
			"cpu": types.Float64Value(a.CPU), "memory_gb": types.Float64Value(a.MemoryGB), "disk_gb": types.Int64Value(a.DiskGB),
			"min_pod_cpu": opt(a.MinPodCPU), "min_pod_memory_gb": opt(a.MinPodMemoryGB),
		})
		diags.Append(d...)
		m.Auto = v
	}
	poolType := types.ObjectType{AttrTypes: poolTypes}
	m.Pools = types.ListNull(poolType)
	if len(cl.Spec.Pools) > 0 {
		var elems []attr.Value
		for _, p := range cl.Spec.Pools {
			v, d := types.ObjectValue(poolTypes, map[string]attr.Value{
				"name": types.StringValue(p.Name), "count": types.Int64Value(p.Count), "cpu": types.Float64Value(p.CPU),
				"memory_gb": types.Float64Value(p.MemoryGB), "disk_gb": types.Int64Value(p.DiskGB),
			})
			diags.Append(d...)
			elems = append(elems, v)
		}
		v, d := types.ListValue(poolType, elems)
		diags.Append(d...)
		m.Pools = v
	}
	m.Status = types.StringValue(cl.Status)
	hosts := make([]attr.Value, 0, len(cl.PublicHosts))
	for _, h := range cl.PublicHosts {
		hosts = append(hosts, types.StringValue(h))
	}
	hv, d := types.ListValue(types.StringType, hosts)
	diags.Append(d...)
	m.PublicHosts = hv
	m.CPUMilli = types.Int64Value(cl.Size.CPUMilli)
	m.MemoryBytes = types.Int64Value(cl.Size.MemoryBytes)
	m.DiskBytes = types.Int64Value(cl.Size.DiskBytes)
	m.HourlyMicro = types.Int64Value(cl.HourlyMicro)
	m.CreatedAt = types.StringValue(cl.CreatedAt)
	return diags
}

func (r *clusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, d := plan.spec(ctx)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	cl, err := r.c.CreateCluster(ctx, plan.Name.ValueString(), spec)
	if err != nil {
		resp.Diagnostics.AddError("Creating the cluster failed", err.Error())
		return
	}
	// Save the ID at once so a failed or interrupted wait leaves a tainted resource, not an orphan.
	resp.Diagnostics.Append(plan.fill(cl)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	wctx, cancel := context.WithTimeout(ctx, CreateTimeout)
	defer cancel()
	ready, err := r.c.WaitCluster(wctx, cl.ID, false, PollInterval)
	if ready != nil {
		resp.Diagnostics.Append(plan.fill(ready)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("The cluster did not become ready", err.Error())
	}
}

func (r *clusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cl, err := r.c.GetCluster(ctx, state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && cl.Status == client.StatusDeleting) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the cluster failed", err.Error())
		return
	}
	resp.Diagnostics.Append(state.fill(cl)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *clusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state clusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, d := plan.spec(ctx)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if spec.Mode == "autopilot" && spec.Autopilot == nil {
		// The cap was removed from the configuration: keep the current one (the API needs a value).
		old, _ := state.spec(ctx)
		spec.Autopilot = old.Autopilot
	}
	id := state.ID.ValueString()
	if _, err := r.c.UpdateSpec(ctx, id, spec); err != nil {
		resp.Diagnostics.AddError("Resizing the cluster failed", err.Error())
		return
	}
	wctx, cancel := context.WithTimeout(ctx, CreateTimeout)
	defer cancel()
	cl, err := r.c.WaitCluster(wctx, id, false, PollInterval)
	if cl != nil {
		resp.Diagnostics.Append(plan.fill(cl)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("The cluster did not become ready after resizing", err.Error())
	}
}

func (r *clusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.c.DeleteCluster(ctx, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting the cluster failed", err.Error())
		return
	}
	wctx, cancel := context.WithTimeout(ctx, DeleteTimeout)
	defer cancel()
	if _, err := r.c.WaitCluster(wctx, id, true, PollInterval); err != nil {
		resp.Diagnostics.AddError("The cluster was not deleted in time", err.Error())
	}
}

func (r *clusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
