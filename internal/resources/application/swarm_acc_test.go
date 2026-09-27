package application_test

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/vanillauys/terraform-provider-dokploy/internal/acctest"
	"github.com/vanillauys/terraform-provider-dokploy/internal/client"
)

// TestAccApplication_swarm walks the swarm block (#69) through its
// lifecycle, asserting the server with a direct read at every step:
//
//   - a plan-time rejection of a mode with two kinds set;
//   - a create with nine columns set, deployed, so Docker itself accepts the
//     spec; the data source reports the same block;
//   - a change that drops four columns, which the server must clear;
//   - removing the block, which clears every column and plans empty;
//   - a value set outside Terraform while the block is absent, which the
//     refresh does not show and an unrelated update leaves alone;
//   - an import, which fills the block from that value.
func TestAccApplication_swarm(t *testing.T) {
	name := acctest.RandomName("app-swarm")
	app := func(deploy bool, attrs string) string {
		deployLine := "  deploy_on_change = false\n"
		if deploy {
			deployLine = ""
		}
		return fmt.Sprintf(`
resource "dokploy_project" "test" {
  name = %q
}

resource "dokploy_application" "test" {
  name           = %q
  environment_id = dokploy_project.test.environments[0].id
  docker         = { image = "traefik/whoami:v1.10" }
%s%s
}`, name+"-proj", name, deployLine, attrs)
	}
	full := app(true, `
  swarm = {
    health_check   = { test = ["NONE"] }
    restart_policy = { condition = "on-failure", delay = 5000000000, max_attempts = 3 }
    placement = {
      constraints  = ["node.role == manager"]
      preferences  = [{ spread = "node.id" }]
      max_replicas = 2
    }
    update_config = {
      parallelism       = 1
      order             = "start-first"
      failure_action    = "rollback"
      delay             = 1000000000
      monitor           = 10000000000
      max_failure_ratio = 0.5
    }
    rollback_config   = { parallelism = 1, order = "stop-first" }
    mode              = { replicated = { replicas = 1 } }
    labels            = { team = "acc" }
    network           = [{ target = "dokploy-network", aliases = ["acc-swarm"] }]
    stop_grace_period = 5000000000
    endpoint_spec     = { mode = "dnsrr" }
    ulimits           = [{ name = "nofile", soft = 1024, hard = 2048 }]
  }
`) + `

data "dokploy_application" "test" {
  id = dokploy_application.test.id
}`
	changed := app(false, `
  swarm = {
    restart_policy  = { condition = "any" }
    placement       = { platforms = [{ architecture = "amd64", os = "linux" }] }
    update_config   = { parallelism = 2, order = "stop-first" }
    rollback_config = { parallelism = 1, order = "stop-first" }
    mode            = { global = {} }
    stop_grace_period = 1000000000
    endpoint_spec   = { mode = "vip" }
  }
`)
	emptyPlan := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	const addr = "dokploy_application.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		CheckDestroy:             checkApplicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:      app(false, `  swarm = { mode = { replicated = {}, global = {} } }`),
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
			{
				Config: full,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "status", "done"),
					resource.TestCheckResourceAttr(addr, "swarm.placement.preferences.0.spread", "node.id"),
					resource.TestCheckResourceAttr("data.dokploy_application.test", "swarm.placement.constraints.0", "node.role == manager"),
					resource.TestCheckResourceAttr("data.dokploy_application.test", "swarm.ulimits.0.hard", "2048"),
					resource.TestCheckNoResourceAttr("data.dokploy_application.test", "swarm.health_check.interval"),
					fetchApplication(func(a *client.Application) error {
						s := a.Swarm
						switch {
						case s.HealthCheckSwarm == nil || len(s.HealthCheckSwarm.Test) != 1:
							return fmt.Errorf("server healthCheckSwarm = %+v", s.HealthCheckSwarm)
						case s.RestartPolicySwarm == nil || *s.RestartPolicySwarm.Condition != "on-failure" || *s.RestartPolicySwarm.MaxAttempts != 3:
							return fmt.Errorf("server restartPolicySwarm = %+v", s.RestartPolicySwarm)
						case s.PlacementSwarm == nil || s.PlacementSwarm.Preferences[0].Spread != "node.id" || *s.PlacementSwarm.MaxReplicas != 2:
							return fmt.Errorf("server placementSwarm = %+v", s.PlacementSwarm)
						case s.UpdateConfigSwarm == nil || s.UpdateConfigSwarm.Order != "start-first" || *s.UpdateConfigSwarm.MaxFailureRatio != 0.5:
							return fmt.Errorf("server updateConfigSwarm = %+v", s.UpdateConfigSwarm)
						case s.ModeSwarm == nil || s.ModeSwarm.Replicated == nil || *s.ModeSwarm.Replicated.Replicas != 1:
							return fmt.Errorf("server modeSwarm = %+v", s.ModeSwarm)
						case s.LabelsSwarm["team"] != "acc" || len(s.NetworkSwarm) != 1 || s.NetworkSwarm[0].Aliases[0] != "acc-swarm":
							return fmt.Errorf("server labels %v network %+v", s.LabelsSwarm, s.NetworkSwarm)
						case s.StopGracePeriodSwarm == nil || *s.StopGracePeriodSwarm != 5000000000:
							return fmt.Errorf("server stopGracePeriodSwarm = %v", s.StopGracePeriodSwarm)
						case s.EndpointSpecSwarm == nil || *s.EndpointSpecSwarm.Mode != "dnsrr" || len(s.UlimitsSwarm) != 1:
							return fmt.Errorf("server endpointSpec %+v ulimits %+v", s.EndpointSpecSwarm, s.UlimitsSwarm)
						}
						return nil
					}),
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// The columns the block no longer sets are cleared, not kept.
				Config: changed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "swarm.health_check"),
					resource.TestCheckNoResourceAttr(addr, "swarm.labels"),
					resource.TestCheckResourceAttr(addr, "swarm.placement.platforms.0.os", "linux"),
					fetchApplication(func(a *client.Application) error {
						s := a.Swarm
						if s.HealthCheckSwarm != nil || s.LabelsSwarm != nil || s.NetworkSwarm != nil || s.UlimitsSwarm != nil {
							return fmt.Errorf("server kept dropped columns: health %+v labels %v network %+v ulimits %+v",
								s.HealthCheckSwarm, s.LabelsSwarm, s.NetworkSwarm, s.UlimitsSwarm)
						}
						if s.ModeSwarm == nil || s.ModeSwarm.Global == nil || s.ModeSwarm.Replicated != nil {
							return fmt.Errorf("server modeSwarm = %+v, want global", s.ModeSwarm)
						}
						if s.PlacementSwarm == nil || s.PlacementSwarm.Constraints != nil || s.PlacementSwarm.Platforms[0].Architecture != "amd64" {
							return fmt.Errorf("server placementSwarm = %+v", s.PlacementSwarm)
						}
						return nil
					}),
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// Removing the block clears every column once.
				Config: app(false, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "swarm"),
					fetchApplication(func(a *client.Application) error {
						if !reflect.ValueOf(a.Swarm).IsZero() {
							return fmt.Errorf("server swarm = %+v, want every column null", a.Swarm)
						}
						return nil
					}),
					// Then a label set outside Terraform, the way the Dokploy
					// UI does: application.update with nothing else in it.
					func(s *terraform.State) error {
						c, err := acctest.ClientFromEnv()
						if err != nil {
							return err
						}
						body := map[string]any{"applicationId": s.RootModule().Resources[addr].Primary.ID, "labelsSwarm": map[string]string{"ui": "1"}}
						return c.Post(context.Background(), "/application.update", body, nil)
					},
				),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// With the block absent, an unrelated update must not send the
				// swarm keys: the label set above survives.
				Config: app(false, `  description = "unrelated change"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(addr, "swarm"),
					fetchApplication(func(a *client.Application) error {
						if a.LabelsSwarm["ui"] != "1" {
							return fmt.Errorf("server labelsSwarm = %v, want the UI label to survive", a.LabelsSwarm)
						}
						return nil
					}),
				),
			},
			{
				Config:           app(true, `  description = "unrelated change"`),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// The import shows the UI value, so the plan after it does.
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"swarm"},
				ImportStateCheck:        acctest.ImportStateAttr("swarm.labels.ui", "1"),
			},
		},
	})
}

// TestAccApplication_upgradeFromV1_7 proves that a state written by v1.7.0,
// which has no swarm block, loads with an empty plan: Read keeps the block
// null whatever the server holds.
func TestAccApplication_upgradeFromV1_7(t *testing.T) {
	acctest.SkipWithoutTerraformRegistry(t)
	name := acctest.RandomName("app-up17")
	cfg := fmt.Sprintf(`
resource "dokploy_project" "test" {
  name = %q
}

resource "dokploy_application" "test" {
  name             = %q
  environment_id   = dokploy_project.test.production_environment_id
  deploy_on_change = false
  docker           = { image = "traefik/whoami:v1.10" }
}
`, name+"-proj", name)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { acctest.PreCheck(t) },
		CheckDestroy: checkApplicationDestroy,
		Steps: []resource.TestStep{
			{
				ExternalProviders: map[string]resource.ExternalProvider{
					"dokploy": {Source: "vanillauys/dokploy", VersionConstraint: "1.7.0"},
				},
				Config: cfg,
				Check:  resource.TestCheckResourceAttrSet("dokploy_application.test", "id"),
			},
			{
				ProtoV6ProviderFactories: acctest.ProviderFactories(),
				Config:                   cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("dokploy_application.test", "swarm"),
			},
		},
	})
}
