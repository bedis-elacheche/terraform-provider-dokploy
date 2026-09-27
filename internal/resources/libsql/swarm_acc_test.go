package libsql_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vanillauys/terraform-provider-dokploy/internal/acctest"
	"github.com/vanillauys/terraform-provider-dokploy/internal/client"
)

// TestAccLibsql_swarm covers the eight-column swarm block (#69) on libsql:
// a create with the block set (libsql.create takes none of it, so the
// follow-up update must land it), a change that clears the dropped
// columns, the removal of the block, a value set outside Terraform that an
// unrelated update leaves alone, and an import that shows it.
// deploy_on_change is false throughout (see libsqlWriteOnlyConfig), so the
// import checks the block rather than verifying every attribute.
func TestAccLibsql_swarm(t *testing.T) {
	name := acctest.RandomName("ls-swarm")
	const addr = "dokploy_libsql.test"
	cfg := func(attrs string) string {
		return libsqlWriteOnlyConfig(name, "  database_password = \"acc-password-1\"\n"+attrs)
	}
	server := func(fn func(client.SwarmBase) error) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			got, err := getLibsql(s, addr)
			if err != nil {
				return err
			}
			return fn(got.SwarmBase)
		}
	}
	emptyPlan := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		CheckDestroy:             checkLibsqlDestroy,
		Steps: []resource.TestStep{
			{
				Config: cfg(`
  swarm = {
    placement     = { constraints = ["node.role == manager"] }
    update_config = { parallelism = 1, order = "stop-first" }
    labels        = { team = "acc" }
    mode          = { replicated = { replicas = 1 } }
  }`),
				Check: server(func(s client.SwarmBase) error {
					if s.PlacementSwarm == nil || s.UpdateConfigSwarm == nil || s.LabelsSwarm["team"] != "acc" || s.ModeSwarm == nil {
						return fmt.Errorf("server swarm = %+v", s)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: cfg(`
  swarm = {
    restart_policy = { condition = "any" }
  }`),
				Check: server(func(s client.SwarmBase) error {
					if s.RestartPolicySwarm == nil || s.PlacementSwarm != nil || s.LabelsSwarm != nil || s.ModeSwarm != nil {
						return fmt.Errorf("server swarm = %+v, want only restartPolicySwarm", s)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "swarm"),
					server(func(s client.SwarmBase) error {
						if !reflect.ValueOf(s).IsZero() {
							return fmt.Errorf("server swarm = %+v, want every column null", s)
						}
						return nil
					}),
					func(s *terraform.State) error {
						c, err := acctest.ClientFromEnv()
						if err != nil {
							return err
						}
						body := map[string]any{"libsqlId": s.RootModule().Resources[addr].Primary.ID, "labelsSwarm": map[string]string{"ui": "1"}}
						return c.Post(context.Background(), "/libsql.update", body, nil)
					},
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: cfg(`  description = "unrelated change"`),
				Check: server(func(s client.SwarmBase) error {
					if s.LabelsSwarm["ui"] != "1" {
						return fmt.Errorf("server labelsSwarm = %v, want the UI label to survive", s.LabelsSwarm)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				ResourceName:     addr,
				ImportState:      true,
				ImportStateCheck: acctest.ImportStateAttr("swarm.labels.ui", "1"),
			},
		},
	})
}

// TestAccLibsql_upgradeFromV1_7: a v1.7.0 state has no swarm block and
// loads with an empty plan.
func TestAccLibsql_upgradeFromV1_7(t *testing.T) {
	acctest.SkipWithoutTerraformRegistry(t)
	name := acctest.RandomName("ls-up17")
	config := libsqlWriteOnlyConfig(name, "  database_password = \"acc-password-1\"")
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { acctest.PreCheck(t) },
		CheckDestroy: checkLibsqlDestroy,
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"dokploy": {Source: "vanillauys/dokploy", VersionConstraint: "1.7.0"},
				},
				Config: config,
				Check:  resource.TestCheckResourceAttrSet("dokploy_libsql.test", "id"),
			},
			{
				ProtoV6ProviderFactories: acctest.ProviderFactories(),
				Config:                   config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("dokploy_libsql.test", "swarm"),
			},
		},
	})
}
