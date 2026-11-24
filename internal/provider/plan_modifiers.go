package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// keepIfSpecUnchanged keeps a value derived from the cluster spec (size, price) from the state when
// the spec does not change, and leaves it unknown when it does (a resize recomputes it).
//
// UseStateForUnknown would be wrong here: after a resize the API returns a new size. Without this,
// leaving out the optional `autopilot` block made every plan show "known after apply".
type keepIfSpecUnchanged struct{}

func (keepIfSpecUnchanged) Description(context.Context) string {
	return "Keeps the prior value unless the cluster spec changes."
}

func (k keepIfSpecUnchanged) MarkdownDescription(ctx context.Context) string {
	return k.Description(ctx)
}

func (keepIfSpecUnchanged) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || !req.PlanValue.IsUnknown() || !req.ConfigValue.IsNull() {
		return
	}
	var cfg, plan, state clusterModel
	if req.Config.Get(ctx, &cfg).HasError() || req.Plan.Get(ctx, &plan).HasError() || req.State.Get(ctx, &state).HasError() {
		return
	}
	same := plan.Mode.Equal(state.Mode) && plan.Auto.Equal(state.Auto) && plan.Pools.Equal(state.Pools) &&
		(cfg.Autopilot.IsNull() || cfg.Autopilot.Equal(state.Autopilot))
	if same {
		resp.PlanValue = req.StateValue
	}
}
