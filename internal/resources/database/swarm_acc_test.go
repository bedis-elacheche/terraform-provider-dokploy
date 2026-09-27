// The swarm block (#69) on the database resources. Proven on postgres for
// all five engines on the reasoning of operational_acc_test.go: kind.go
// defines the attribute once, model.go and resource.go carry it once, and
// TestKindClient_NetworkMapping_Expand and _Flatten prove every engine's
// adapter passes it through in both directions.
package database_test

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

// TestAccDatabase_swarm: a deployed create with the block set, a change
// that must clear the dropped columns, the removal of the block, a value set
// outside Terraform that an unrelated update leaves alone, and an import
// that shows it.
func TestAccDatabase_swarm(t *testing.T) {
	name := acctest.RandomName("pg-swarm")
	const addr = "dokploy_postgres.test"
	pg := func(deploy bool, attrs string) string {
		deployLine := "  deploy_on_change  = false\n"
		if deploy {
			deployLine = ""
		}
		return fmt.Sprintf(`
resource "dokploy_project" "test" {
  name = %q
}

resource "dokploy_postgres" "test" {
  name              = %q
  environment_id    = dokploy_project.test.production_environment_id
  database_name     = "acc"
  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "postgres:16-alpine"
%s%s
}`, name+"-proj", name, deployLine, attrs)
	}
	server := func(fn func(client.Swarm) error) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			got, err := getAccPostgres(s)
			if err != nil {
				return err
			}
			return fn(got.Swarm)
		}
	}
	emptyPlan := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		CheckDestroy:             checkPostgresDestroy,
		Steps: []resource.TestStep{
			{
				Config: pg(true, `
  swarm = {
    placement         = { constraints = ["node.role == manager"] }
    update_config     = { parallelism = 1, order = "stop-first", failure_action = "rollback" }
    restart_policy    = { condition = "on-failure" }
    labels            = { team = "acc" }
    stop_grace_period = 20000000000
    endpoint_spec     = { mode = "dnsrr" }
    ulimits           = [{ name = "nofile", soft = 4096, hard = 8192 }]
  }
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "status", "done"),
					server(func(s client.Swarm) error {
						if s.PlacementSwarm == nil || s.PlacementSwarm.Constraints[0] != "node.role == manager" ||
							s.UpdateConfigSwarm == nil || s.UpdateConfigSwarm.Order != "stop-first" ||
							s.LabelsSwarm["team"] != "acc" || s.StopGracePeriodSwarm == nil || *s.StopGracePeriodSwarm != 20000000000 ||
							len(s.UlimitsSwarm) != 1 || s.UlimitsSwarm[0].Hard != 8192 {
							return fmt.Errorf("server swarm = %+v", s)
						}
						return nil
					}),
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: pg(false, `
  swarm = {
    mode = { replicated = { replicas = 1 } }
  }
`),
				Check: server(func(s client.Swarm) error {
					if s.ModeSwarm == nil || s.PlacementSwarm != nil || s.UpdateConfigSwarm != nil || s.LabelsSwarm != nil || s.UlimitsSwarm != nil {
						return fmt.Errorf("server swarm = %+v, want only modeSwarm", s)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: pg(false, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "swarm"),
					server(func(s client.Swarm) error {
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
						body := map[string]any{"postgresId": s.RootModule().Resources[addr].Primary.ID, "labelsSwarm": map[string]string{"ui": "1"}}
						return c.Post(context.Background(), "/postgres.update", body, nil)
					},
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: pg(false, `  description = "unrelated change"`),
				Check: server(func(s client.Swarm) error {
					if s.LabelsSwarm["ui"] != "1" {
						return fmt.Errorf("server labelsSwarm = %v, want the UI label to survive", s.LabelsSwarm)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config:           pg(true, `  description = "unrelated change"`),
				ConfigPlanChecks: emptyPlan,
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"swarm"},
				ImportStateCheck:        acctest.ImportStateAttr("swarm.labels.ui", "1"),
			},
		},
	})
}

// TestAccPostgres_upgradeFromV1_7: a v1.7.0 state has no swarm block and
// loads with an empty plan.
func TestAccPostgres_upgradeFromV1_7(t *testing.T) {
	acctest.SkipWithoutTerraformRegistry(t)
	name := acctest.RandomName("pg-up17")
	config := fmt.Sprintf(`
resource "dokploy_project" "test" {
  name = %q
}

resource "dokploy_postgres" "test" {
  name              = %q
  environment_id    = dokploy_project.test.production_environment_id
  database_name     = "acc"
  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "postgres:16-alpine"
  deploy_on_change  = false
}`, name+"-proj", name)
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { acctest.PreCheck(t) },
		CheckDestroy: checkPostgresDestroy,
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"dokploy": {Source: "vanillauys/dokploy", VersionConstraint: "1.7.0"},
				},
				Config: config,
				Check:  resource.TestCheckResourceAttrSet("dokploy_postgres.test", "id"),
			},
			{
				ProtoV6ProviderFactories: acctest.ProviderFactories(),
				Config:                   config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("dokploy_postgres.test", "swarm"),
			},
		},
	})
}
