// Package swarm is the `swarm` block (#69): the Docker Swarm service
// settings that dokploy_application and every database resource carry, and
// the two data sources expose. One schema, one mapping, seven resources.
//
// The block is plain Optional, not Computed (the MarkComputedNilsAsUnknown
// trap on `build` in resources/application/resource.go). Its semantics:
//
//   - A null block is unmanaged. Expand returns nil and the update body
//     leaves every swarm key out, so a value set in the Dokploy UI survives
//     (dialect B). Read keeps a null block null, so a state from a release
//     before the block plans no change.
//   - A block that is set owns every column in it: a column the config
//     leaves out is sent as null and cleared.
//   - Removing a block that was set sends a null for every column once, on
//     the apply that removes it (Expand sees the prior block in the state).
//   - Import fills the block from the server when any column is set, so the
//     plan after an import shows the real settings.
//
// The attribute tree maps onto client.Swarm / client.SwarmBase through the
// framework's reflection: those structs carry the `tfsdk` tags.
package swarm

import (
	"context"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const ns = " In nanoseconds: `10000000000` is 10 seconds."

const noEmpty = " An empty list is not valid. Omit the attribute instead."

func str(desc string, oneOf ...string) schema.StringAttribute {
	a := schema.StringAttribute{Optional: true, Description: desc}
	if len(oneOf) > 0 {
		a.Validators = []validator.String{stringvalidator.OneOf(oneOf...)}
	}
	return a
}

func num(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, Description: desc}
}

func strList(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		Optional: true, ElementType: types.StringType, Description: desc + noEmpty,
		Validators: []validator.List{listvalidator.SizeAtLeast(1)},
	}
}

func object(desc string, attrs map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Optional: true, Description: desc, Attributes: attrs}
}

func objectList(desc string, attrs map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional: true, Description: desc + noEmpty,
		NestedObject: schema.NestedAttributeObject{Attributes: attrs},
		Validators:   []validator.List{listvalidator.SizeAtLeast(1)},
	}
}

func updateConfig(verb, verbs string) schema.SingleNestedAttribute {
	return object("How Docker "+verbs+" the service's tasks.", map[string]schema.Attribute{
		"parallelism": schema.Int64Attribute{Required: true, Description: "Tasks to " + verb + " at once. `0` means all at once."},
		"order":       schema.StringAttribute{Required: true, Description: "`stop-first` or `start-first`.", Validators: []validator.String{stringvalidator.OneOf("stop-first", "start-first")}},
		"delay":       num("Wait between batches." + ns),
		"failure_action": str("What to do when a task fails: `pause`, `continue`, or `rollback`.",
			"pause", "continue", "rollback"),
		"monitor":           num("How long to watch each task for failure after it starts." + ns),
		"max_failure_ratio": schema.Float64Attribute{Optional: true, Description: "Fraction of tasks that may fail before `failure_action` applies."},
	})
}

// Attribute is the resource's `swarm` attribute. libsql takes the eight
// columns its deploy builder applies (client.SwarmBase); every other
// resource takes all eleven (client.Swarm).
func Attribute(libsql bool) schema.SingleNestedAttribute {
	emptyMode := func(desc string) schema.SingleNestedAttribute {
		return object(desc+" Write it as `{}`.", map[string]schema.Attribute{})
	}
	attrs := map[string]schema.Attribute{
		"health_check": object("Health check, replacing the image's `HEALTHCHECK`.", map[string]schema.Attribute{
			"test":         strList("Command, in Docker's form: `[\"CMD\", \"curl\", \"-f\", \"http://localhost\"]`, `[\"CMD-SHELL\", \"...\"]`, or `[\"NONE\"]`."),
			"interval":     num("Time between checks." + ns),
			"timeout":      num("Time a check may take." + ns),
			"start_period": num("Start-up time during which failures do not count." + ns),
			"retries":      num("Consecutive failures that make the task unhealthy."),
		}),
		"restart_policy": object("When Docker restarts the service's tasks.", map[string]schema.Attribute{
			"condition":    str("`none`, `on-failure`, or `any`.", "none", "on-failure", "any"),
			"delay":        num("Wait between restart attempts." + ns),
			"max_attempts": num("Restart attempts before giving up. `0` means no limit."),
			"window":       num("Time used to decide whether a restart succeeded." + ns),
		}),
		"placement": object("Where the service's tasks run.", map[string]schema.Attribute{
			"constraints": strList("Placement constraints, for example `node.labels.tier == app`."),
			"preferences": objectList("Spread preferences, applied in order.", map[string]schema.Attribute{
				"spread": schema.StringAttribute{Required: true, Description: "Label to spread tasks over, for example `node.labels.zone`."},
			}),
			"max_replicas": num("Maximum tasks per node. `0` means no limit."),
			"platforms": objectList("Platforms the tasks may run on.", map[string]schema.Attribute{
				"architecture": schema.StringAttribute{Required: true, Description: "CPU architecture, for example `amd64`."},
				"os":           schema.StringAttribute{Required: true, Description: "Operating system, for example `linux`."},
			}),
		}),
		"update_config":   updateConfig("update", "updates"),
		"rollback_config": updateConfig("roll back", "rolls back"),
		"mode": object("Service mode. Set exactly one of the four attributes. When set, it replaces the `replicas` attribute of the resource.", map[string]schema.Attribute{
			"replicated": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Run a fixed number of tasks.",
				Attributes:  map[string]schema.Attribute{"replicas": num("Number of tasks.")},
				Validators: []validator.Object{objectvalidator.ExactlyOneOf(
					path.MatchRelative().AtParent().AtName("global"),
					path.MatchRelative().AtParent().AtName("replicated_job"),
					path.MatchRelative().AtParent().AtName("global_job"),
				)},
			},
			"global": emptyMode("Run one task on every node."),
			"replicated_job": object("Run a job to completion.", map[string]schema.Attribute{
				"max_concurrent":    num("Tasks that run at once."),
				"total_completions": num("Tasks that must complete."),
			}),
			"global_job": emptyMode("Run a job once on every node."),
		}),
		"labels": schema.MapAttribute{
			Optional: true, ElementType: types.StringType,
			Description: "Labels on the service's containers.",
		},
		"network": schema.ListNestedAttribute{
			Optional: true,
			Description: "Networks to attach, with aliases and driver options. When set, Dokploy attaches exactly these networks: " +
				"`dokploy-network` is not added, so list it here if the service needs Traefik routing. " +
				"It cannot be combined with `network_ids` or `detach_dokploy_network`.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"target":  str("Network name or id."),
				"aliases": strList("DNS aliases of the service on this network."),
				"driver_opts": schema.MapAttribute{
					Optional: true, ElementType: types.StringType,
					Description: "Network driver options. An empty map is not valid. Omit the attribute instead.",
					Validators:  []validator.Map{mapvalidator.SizeAtLeast(1)},
				},
			}},
			Validators: []validator.List{listvalidator.ConflictsWith(
				path.MatchRoot("network_ids"), path.MatchRoot("detach_dokploy_network"),
			)},
		},
	}
	desc := "Docker Swarm service settings, applied on the next deploy (a change starts one when `deploy_on_change` is true). " +
		"Omit the block to leave these settings unmanaged: the provider does not read or write them, and values set in the Dokploy UI stay. " +
		"When the block is set it owns every setting in it, and an attribute you omit is cleared back to the Dokploy default. " +
		"Removing the block clears them all."
	if !libsql {
		attrs["stop_grace_period"] = num("Time to wait for a task to stop before killing it." + ns)
		attrs["endpoint_spec"] = object("How the service is reached. Replaces the endpoint spec Dokploy derives from the service's ports.", map[string]schema.Attribute{
			"mode": str("`vip` or `dnsrr`.", "vip", "dnsrr"),
			"ports": objectList("Published ports.", map[string]schema.Attribute{
				"protocol":       str("`tcp`, `udp`, or `sctp`.", "tcp", "udp", "sctp"),
				"target_port":    num("Container port."),
				"published_port": num("Port published on the swarm."),
				"publish_mode":   str("`ingress` or `host`.", "ingress", "host"),
			}),
		})
		attrs["ulimits"] = schema.ListNestedAttribute{
			Optional:    true,
			Description: "Resource limits of the service's containers.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{Required: true, Description: "Limit name, for example `nofile`.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
				"soft": schema.Int64Attribute{Required: true, Description: "Soft limit. `-1` means unlimited.", Validators: []validator.Int64{int64validator.AtLeast(-1)}},
				"hard": schema.Int64Attribute{Required: true, Description: "Hard limit. `-1` means unlimited.", Validators: []validator.Int64{int64validator.AtLeast(-1)}},
			}},
		}
	} else {
		desc += " libsql deploys eight of the Docker settings: it ignores the stop grace period and the endpoint spec, and has no ulimits."
	}
	return schema.SingleNestedAttribute{Optional: true, Description: desc, Attributes: attrs}
}

// DataSourceAttribute is the Computed twin of Attribute for the data
// sources: the same tree, without validators. A data source reports the
// block whenever any column is set, and null otherwise.
func DataSourceAttribute(libsql bool) dsschema.SingleNestedAttribute {
	a := Attribute(libsql)
	return dsschema.SingleNestedAttribute{
		Computed:    true,
		Description: "Docker Swarm service settings. Null when none is set.",
		Attributes:  dsAttributes(a.Attributes),
	}
}

func dsAttributes(in map[string]schema.Attribute) map[string]dsschema.Attribute {
	out := make(map[string]dsschema.Attribute, len(in))
	for name, a := range in {
		out[name] = dsAttribute(a)
	}
	return out
}

func dsAttribute(a schema.Attribute) dsschema.Attribute {
	d := a.GetDescription()
	switch a := a.(type) {
	case schema.StringAttribute:
		return dsschema.StringAttribute{Computed: true, Description: d}
	case schema.Int64Attribute:
		return dsschema.Int64Attribute{Computed: true, Description: d}
	case schema.Float64Attribute:
		return dsschema.Float64Attribute{Computed: true, Description: d}
	case schema.ListAttribute:
		return dsschema.ListAttribute{Computed: true, Description: d, ElementType: a.ElementType}
	case schema.MapAttribute:
		return dsschema.MapAttribute{Computed: true, Description: d, ElementType: a.ElementType}
	case schema.SingleNestedAttribute:
		return dsschema.SingleNestedAttribute{Computed: true, Description: d, Attributes: dsAttributes(a.Attributes)}
	case schema.ListNestedAttribute:
		return dsschema.ListNestedAttribute{Computed: true, Description: d,
			NestedObject: dsschema.NestedAttributeObject{Attributes: dsAttributes(a.NestedObject.Attributes)}}
	}
	panic("swarm: no data source mapping for " + reflect.TypeOf(a).String())
}

// AttrTypes is the object type of the attribute, for building values.
func AttrTypes(libsql bool) map[string]attr.Type {
	return Attribute(libsql).GetType().(types.ObjectType).AttrTypes
}

// Expand builds the swarm part of an update body from the planned block and
// the block in the prior state (null on create). T is client.Swarm or
// client.SwarmBase. nil means "leave every key out"; a zero T clears every
// column.
func Expand[T any](ctx context.Context, plan, prior types.Object, diags *diag.Diagnostics) *T {
	if plan.IsNull() || plan.IsUnknown() {
		if prior.IsNull() {
			return nil
		}
		return new(T)
	}
	out := new(T)
	diags.Append(plan.As(ctx, out, basetypes.ObjectAsOptions{})...)
	return out
}

// Value maps the server's columns onto the block, null when none is set.
func Value[T any](ctx context.Context, v T, attrTypes map[string]attr.Type, diags *diag.Diagnostics) types.Object {
	if reflect.ValueOf(v).IsZero() {
		return types.ObjectNull(attrTypes)
	}
	return objectValue(ctx, v, attrTypes, diags)
}

// Read maps the server's columns onto the block for a resource read. prior
// is the block in the state before the read; importing is true on the read
// that follows an import, whose state holds nothing but the id.
func Read[T any](ctx context.Context, v T, prior types.Object, importing bool, attrTypes map[string]attr.Type, diags *diag.Diagnostics) types.Object {
	switch {
	case importing:
		return Value(ctx, v, attrTypes, diags)
	case prior.IsNull():
		return types.ObjectNull(attrTypes)
	}
	return objectValue(ctx, v, attrTypes, diags)
}

func objectValue[T any](ctx context.Context, v T, attrTypes map[string]attr.Type, diags *diag.Diagnostics) types.Object {
	obj, d := types.ObjectValueFrom(ctx, attrTypes, v)
	diags.Append(d...)
	return obj
}
