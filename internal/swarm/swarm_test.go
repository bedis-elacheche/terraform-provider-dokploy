package swarm

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/vanillauys/terraform-provider-dokploy/internal/client"
)

func ptr[T any](v T) *T { return &v }

// full sets every column and every key, so a mapping that drops one fails
// the round trip.
func full() client.Swarm {
	return client.Swarm{
		SwarmBase: client.SwarmBase{
			HealthCheckSwarm: &client.SwarmHealthCheck{
				Test: []string{"CMD", "true"}, Interval: ptr(int64(1e10)), Timeout: ptr(int64(2e9)),
				StartPeriod: ptr(int64(3e9)), Retries: ptr(int64(3)),
			},
			RestartPolicySwarm: &client.SwarmRestartPolicy{
				Condition: ptr("on-failure"), Delay: ptr(int64(5e9)), MaxAttempts: ptr(int64(4)), Window: ptr(int64(6e10)),
			},
			PlacementSwarm: &client.SwarmPlacement{
				Constraints: []string{"node.role == worker"},
				Preferences: []client.SwarmPreference{{Spread: "node.labels.zone"}},
				MaxReplicas: ptr(int64(2)),
				Platforms:   []client.SwarmPlatform{{Architecture: "amd64", OS: "linux"}},
			},
			UpdateConfigSwarm: &client.SwarmUpdateConfig{
				Parallelism: 1, Delay: ptr(int64(1e9)), FailureAction: ptr("rollback"),
				Monitor: ptr(int64(1e10)), MaxFailureRatio: ptr(0.25), Order: "start-first",
			},
			RollbackConfigSwarm: &client.SwarmUpdateConfig{Parallelism: 2, Order: "stop-first"},
			ModeSwarm:           &client.SwarmMode{Global: &client.SwarmEmpty{}},
			LabelsSwarm:         map[string]string{"team": "api"},
			NetworkSwarm: []client.SwarmNetwork{{
				Target: ptr("dokploy-network"), Aliases: []string{"api"}, DriverOpts: map[string]string{"a": "b"},
			}},
		},
		StopGracePeriodSwarm: ptr(int64(3e10)),
		EndpointSpecSwarm: &client.SwarmEndpointSpec{Mode: ptr("vip"), Ports: []client.SwarmPortConfig{{
			Protocol: ptr("tcp"), TargetPort: ptr(int64(80)), PublishedPort: ptr(int64(8080)), PublishMode: ptr("host"),
		}}},
		UlimitsSwarm: []client.SwarmUlimit{{Name: "nofile", Soft: 1024, Hard: -1}},
	}
}

func check(t *testing.T, diags diag.Diagnostics) {
	t.Helper()
	if diags.HasError() {
		t.Fatal(diags)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics
	want := full()
	obj := Value(ctx, want, AttrTypes(false), &diags)
	check(t, diags)
	got := Expand[client.Swarm](ctx, obj, types.ObjectNull(AttrTypes(false)), &diags)
	check(t, diags)
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", *got, want)
	}

	base := want.SwarmBase
	obj = Value(ctx, base, AttrTypes(true), &diags)
	check(t, diags)
	gotBase := Expand[client.SwarmBase](ctx, obj, types.ObjectNull(AttrTypes(true)), &diags)
	check(t, diags)
	if !reflect.DeepEqual(*gotBase, base) {
		t.Fatalf("libsql round trip:\n got %+v\nwant %+v", *gotBase, base)
	}
}

// The wire shape: Docker's nested Spread preference, and null for an unset
// column, which clears it on a dialect B endpoint.
func TestWireShape(t *testing.T) {
	b, err := json.Marshal(client.SwarmBase{
		PlacementSwarm: &client.SwarmPlacement{Preferences: []client.SwarmPreference{{Spread: "node.labels.zone"}}},
		ModeSwarm:      &client.SwarmMode{GlobalJob: &client.SwarmEmpty{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"healthCheckSwarm":null,"restartPolicySwarm":null,` +
		`"placementSwarm":{"Preferences":[{"Spread":{"SpreadDescriptor":"node.labels.zone"}}]},` +
		`"updateConfigSwarm":null,"rollbackConfigSwarm":null,"modeSwarm":{"GlobalJob":{}},` +
		`"labelsSwarm":null,"networkSwarm":null}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	var back client.SwarmBase
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.PlacementSwarm.Preferences[0].Spread != "node.labels.zone" {
		t.Fatalf("preference did not decode: %+v", back.PlacementSwarm)
	}
}

// A nil *Swarm embedded in an update request leaves every swarm key out.
func TestNilEmbedOmitsKeys(t *testing.T) {
	b, err := json.Marshal(client.UpdatePostgresRequest{PostgresID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if len(k) > 5 && k[len(k)-5:] == "Swarm" {
			t.Errorf("nil swarm sent %q", k)
		}
	}
}

func TestExpand(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics
	null := types.ObjectNull(AttrTypes(false))
	set := Value(ctx, full(), AttrTypes(false), &diags)
	check(t, diags)

	if got := Expand[client.Swarm](ctx, null, null, &diags); got != nil {
		t.Errorf("unmanaged block: got %+v, want nil (keys left out)", got)
	}
	if got := Expand[client.Swarm](ctx, null, set, &diags); got == nil || !reflect.ValueOf(*got).IsZero() {
		t.Errorf("removed block: got %+v, want a zero Swarm (every column cleared)", got)
	}
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics
	attrTypes := AttrTypes(false)
	null := types.ObjectNull(attrTypes)
	empty := Value(ctx, client.Swarm{SwarmBase: client.SwarmBase{LabelsSwarm: map[string]string{}}}, attrTypes, &diags)
	check(t, diags)

	cases := []struct {
		name      string
		server    client.Swarm
		prior     types.Object
		importing bool
		wantNull  bool
	}{
		{"unmanaged stays null despite UI values", full(), null, false, true},
		{"managed block with nothing set stays a block", client.Swarm{}, empty, false, false},
		{"import with values fills the block", full(), null, true, false},
		{"import with nothing set is null", client.Swarm{}, null, true, true},
	}
	for _, tc := range cases {
		got := Read(ctx, tc.server, tc.prior, tc.importing, attrTypes, &diags)
		check(t, diags)
		if got.IsNull() != tc.wantNull {
			t.Errorf("%s: null = %v, want %v", tc.name, got.IsNull(), tc.wantNull)
		}
	}
}

func TestDataSourceAttributeMirrorsResource(t *testing.T) {
	for _, libsql := range []bool{false, true} {
		r := Attribute(libsql).GetType()
		d := DataSourceAttribute(libsql).GetType()
		if !r.Equal(d) {
			t.Errorf("libsql=%v: data source type %s differs from resource type %s", libsql, d, r)
		}
	}
}
