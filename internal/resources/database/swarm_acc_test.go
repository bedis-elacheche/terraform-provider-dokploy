// The swarm block (#69) on the five database resources: one shared test
// body, run on every engine, so each engine's adapter and each engine's
// deploy builder is proven against the rig.
package database_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vanillauys/terraform-provider-dokploy/internal/acctest"
	"github.com/vanillauys/terraform-provider-dokploy/internal/client"
)

// swarmEngine is what the shared swarm test needs from one engine: its
// resource type, the config lines of its credentials and image, the id key
// of its .update body, and a server read of its swarm columns.
type swarmEngine struct {
	resourceType string
	attrs        string
	idKey        string
	destroy      resource.TestCheckFunc
	swarm        func(s *terraform.State) (client.Swarm, error)
}

func TestAccPostgres_swarm(t *testing.T) {
	testAccDatabaseSwarm(t, swarmEngine{
		resourceType: "dokploy_postgres",
		attrs: `  database_name     = "acc"
  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "postgres:16-alpine"`,
		idKey:   "postgresId",
		destroy: checkPostgresDestroy,
		swarm: func(s *terraform.State) (client.Swarm, error) {
			o, err := getAccPostgres(s)
			if err != nil {
				return client.Swarm{}, err
			}
			return o.Swarm, nil
		},
	})
}

func TestAccMysql_swarm(t *testing.T) {
	testAccDatabaseSwarm(t, swarmEngine{
		resourceType: "dokploy_mysql",
		attrs: `  database_name     = "acc"
  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "mysql:8"`,
		idKey:   "mysqlId",
		destroy: checkMysqlDestroy,
		swarm: func(s *terraform.State) (client.Swarm, error) {
			o, err := getAccMysql(s)
			if err != nil {
				return client.Swarm{}, err
			}
			return o.Swarm, nil
		},
	})
}

func TestAccMariadb_swarm(t *testing.T) {
	testAccDatabaseSwarm(t, swarmEngine{
		resourceType: "dokploy_mariadb",
		attrs: `  database_name     = "acc"
  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "mariadb:11.4"`,
		idKey:   "mariadbId",
		destroy: checkMariadbDestroy,
		swarm: func(s *terraform.State) (client.Swarm, error) {
			o, err := getAccMariadb(s)
			if err != nil {
				return client.Swarm{}, err
			}
			return o.Swarm, nil
		},
	})
}

func TestAccMongo_swarm(t *testing.T) {
	testAccDatabaseSwarm(t, swarmEngine{
		resourceType: "dokploy_mongo",
		attrs: `  database_user     = "acc"
  database_password = "acc-password-1"
  docker_image      = "mongo:7"`,
		idKey:   "mongoId",
		destroy: checkMongoDestroy,
		swarm: func(s *terraform.State) (client.Swarm, error) {
			o, err := getAccMongo(s)
			if err != nil {
				return client.Swarm{}, err
			}
			return o.Swarm, nil
		},
	})
}

func TestAccRedis_swarm(t *testing.T) {
	testAccDatabaseSwarm(t, swarmEngine{
		resourceType: "dokploy_redis",
		attrs: `  database_password = "acc-password-1"
  docker_image      = "redis:8"`,
		idKey:   "redisId",
		destroy: checkRedisDestroy,
		swarm: func(s *terraform.State) (client.Swarm, error) {
			o, err := getAccRedis(s)
			if err != nil {
				return client.Swarm{}, err
			}
			return o.Swarm, nil
		},
	})
}

// testAccDatabaseSwarm: a deployed create with the block set, so Docker
// accepts the spec; a change that must clear the dropped columns; the
// removal of the block; a value set outside Terraform that an unrelated
// update leaves alone; and an import that shows it.
func testAccDatabaseSwarm(t *testing.T, e swarmEngine) {
	name := acctest.RandomName(strings.TrimPrefix(e.resourceType, "dokploy_") + "-swarm")
	addr := e.resourceType + ".test"
	cfg := func(deploy bool, attrs string) string {
		deployLine := "  deploy_on_change  = false\n"
		if deploy {
			deployLine = ""
		}
		return fmt.Sprintf(`
resource "dokploy_project" "test" {
  name = %q
}

resource %q "test" {
  name              = %q
  environment_id    = dokploy_project.test.production_environment_id
%s
%s%s
}`, name+"-proj", e.resourceType, name, e.attrs, deployLine, attrs)
	}
	server := func(fn func(client.Swarm) error) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			got, err := e.swarm(s)
			if err != nil {
				return err
			}
			return fn(got)
		}
	}
	emptyPlan := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		CheckDestroy:             e.destroy,
		Steps: []resource.TestStep{
			{
				Config: cfg(true, `
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
				Config: cfg(false, `
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
				Config: cfg(false, ""),
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
						body := map[string]any{e.idKey: s.RootModule().Resources[addr].Primary.ID, "labelsSwarm": map[string]string{"ui": "1"}}
						return c.Post(context.Background(), "/"+strings.TrimPrefix(e.resourceType, "dokploy_")+".update", body, nil)
					},
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config: cfg(false, `  description = "unrelated change"`),
				Check: server(func(s client.Swarm) error {
					if s.LabelsSwarm["ui"] != "1" {
						return fmt.Errorf("server labelsSwarm = %v, want the UI label to survive", s.LabelsSwarm)
					}
					return nil
				}),
				ConfigPlanChecks: emptyPlan,
			},
			{
				Config:           cfg(true, `  description = "unrelated change"`),
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
