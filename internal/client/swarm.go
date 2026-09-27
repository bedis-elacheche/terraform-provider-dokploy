package client

import "encoding/json"

// The Docker Swarm service settings (#69): eleven columns that
// application.update and every database engine's .update accept, stored by
// Dokploy verbatim as JSON (drizzle `json`, no defaults) and handed to
// dockerode unchanged on the next deploy. Shapes are the zod schemas of
// Dokploy v0.30.7 (packages/server/src/db/schema/shared.ts, doc.go "v1.8.0
// records"). Every column is nullable on update, and a null clears it; an
// absent key keeps the stored value (dialect B). Durations are Docker
// Engine API nanoseconds.
//
// The structs carry `tfsdk` tags as well as `json` tags: the resources map
// the `swarm` attribute onto them with the framework's reflection, so the
// wire shape and the attribute shape cannot drift apart. The keys inside a
// column are zod `.optional()`, not `.nullable()`: a null there is an HTTP
// 400, so every one of them is omitempty.

// SwarmBase is the eleven-column set minus three: the eight columns libsql's
// deploy builder applies. libsql.update also accepts stopGracePeriodSwarm
// and endpointSpecSwarm, but its builder ignores both (endpoint spec is
// hardcoded to dnsrr) and it has no ulimitsSwarm at all.
type SwarmBase struct {
	HealthCheckSwarm    *SwarmHealthCheck   `json:"healthCheckSwarm" tfsdk:"health_check"`
	RestartPolicySwarm  *SwarmRestartPolicy `json:"restartPolicySwarm" tfsdk:"restart_policy"`
	PlacementSwarm      *SwarmPlacement     `json:"placementSwarm" tfsdk:"placement"`
	UpdateConfigSwarm   *SwarmUpdateConfig  `json:"updateConfigSwarm" tfsdk:"update_config"`
	RollbackConfigSwarm *SwarmUpdateConfig  `json:"rollbackConfigSwarm" tfsdk:"rollback_config"`
	ModeSwarm           *SwarmMode          `json:"modeSwarm" tfsdk:"mode"`
	LabelsSwarm         map[string]string   `json:"labelsSwarm" tfsdk:"labels"`
	NetworkSwarm        []SwarmNetwork      `json:"networkSwarm" tfsdk:"network"`
}

// Swarm is the full eleven-column set of application and the five classic
// engines.
type Swarm struct {
	SwarmBase
	StopGracePeriodSwarm *int64             `json:"stopGracePeriodSwarm" tfsdk:"stop_grace_period"`
	EndpointSpecSwarm    *SwarmEndpointSpec `json:"endpointSpecSwarm" tfsdk:"endpoint_spec"`
	UlimitsSwarm         []SwarmUlimit      `json:"ulimitsSwarm" tfsdk:"ulimits"`
}

type SwarmHealthCheck struct {
	Test        []string `json:"Test,omitempty" tfsdk:"test"`
	Interval    *int64   `json:"Interval,omitempty" tfsdk:"interval"`
	Timeout     *int64   `json:"Timeout,omitempty" tfsdk:"timeout"`
	StartPeriod *int64   `json:"StartPeriod,omitempty" tfsdk:"start_period"`
	Retries     *int64   `json:"Retries,omitempty" tfsdk:"retries"`
}

type SwarmRestartPolicy struct {
	Condition   *string `json:"Condition,omitempty" tfsdk:"condition"`
	Delay       *int64  `json:"Delay,omitempty" tfsdk:"delay"`
	MaxAttempts *int64  `json:"MaxAttempts,omitempty" tfsdk:"max_attempts"`
	Window      *int64  `json:"Window,omitempty" tfsdk:"window"`
}

type SwarmPlacement struct {
	Constraints []string          `json:"Constraints,omitempty" tfsdk:"constraints"`
	Preferences []SwarmPreference `json:"Preferences,omitempty" tfsdk:"preferences"`
	MaxReplicas *int64            `json:"MaxReplicas,omitempty" tfsdk:"max_replicas"`
	Platforms   []SwarmPlatform   `json:"Platforms,omitempty" tfsdk:"platforms"`
}

// SwarmPreference flattens Docker's {"Spread":{"SpreadDescriptor":"..."}},
// the only preference kind there is, to one attribute.
type SwarmPreference struct {
	Spread string `json:"-" tfsdk:"spread"`
}

type swarmPreferenceWire struct {
	Spread struct {
		SpreadDescriptor string `json:"SpreadDescriptor"`
	} `json:"Spread"`
}

func (p SwarmPreference) MarshalJSON() ([]byte, error) {
	var w swarmPreferenceWire
	w.Spread.SpreadDescriptor = p.Spread
	return json.Marshal(w)
}

func (p *SwarmPreference) UnmarshalJSON(b []byte) error {
	var w swarmPreferenceWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	p.Spread = w.Spread.SpreadDescriptor
	return nil
}

type SwarmPlatform struct {
	Architecture string `json:"Architecture" tfsdk:"architecture"`
	OS           string `json:"OS" tfsdk:"os"`
}

// SwarmUpdateConfig is the shape of both updateConfigSwarm and
// rollbackConfigSwarm; zod requires Parallelism and Order.
type SwarmUpdateConfig struct {
	Parallelism     int64    `json:"Parallelism" tfsdk:"parallelism"`
	Delay           *int64   `json:"Delay,omitempty" tfsdk:"delay"`
	FailureAction   *string  `json:"FailureAction,omitempty" tfsdk:"failure_action"`
	Monitor         *int64   `json:"Monitor,omitempty" tfsdk:"monitor"`
	MaxFailureRatio *float64 `json:"MaxFailureRatio,omitempty" tfsdk:"max_failure_ratio"`
	Order           string   `json:"Order" tfsdk:"order"`
}

type SwarmMode struct {
	Replicated    *SwarmReplicated    `json:"Replicated,omitempty" tfsdk:"replicated"`
	Global        *SwarmEmpty         `json:"Global,omitempty" tfsdk:"global"`
	ReplicatedJob *SwarmReplicatedJob `json:"ReplicatedJob,omitempty" tfsdk:"replicated_job"`
	GlobalJob     *SwarmEmpty         `json:"GlobalJob,omitempty" tfsdk:"global_job"`
}

type SwarmReplicated struct {
	Replicas *int64 `json:"Replicas,omitempty" tfsdk:"replicas"`
}

type SwarmReplicatedJob struct {
	MaxConcurrent    *int64 `json:"MaxConcurrent,omitempty" tfsdk:"max_concurrent"`
	TotalCompletions *int64 `json:"TotalCompletions,omitempty" tfsdk:"total_completions"`
}

// SwarmEmpty is the {} of the Global and GlobalJob modes.
type SwarmEmpty struct{}

type SwarmNetwork struct {
	Target     *string           `json:"Target,omitempty" tfsdk:"target"`
	Aliases    []string          `json:"Aliases,omitempty" tfsdk:"aliases"`
	DriverOpts map[string]string `json:"DriverOpts,omitempty" tfsdk:"driver_opts"`
}

type SwarmEndpointSpec struct {
	Mode  *string           `json:"Mode,omitempty" tfsdk:"mode"`
	Ports []SwarmPortConfig `json:"Ports,omitempty" tfsdk:"ports"`
}

type SwarmPortConfig struct {
	Protocol      *string `json:"Protocol,omitempty" tfsdk:"protocol"`
	TargetPort    *int64  `json:"TargetPort,omitempty" tfsdk:"target_port"`
	PublishedPort *int64  `json:"PublishedPort,omitempty" tfsdk:"published_port"`
	PublishMode   *string `json:"PublishMode,omitempty" tfsdk:"publish_mode"`
}

type SwarmUlimit struct {
	Name string `json:"Name" tfsdk:"name"`
	Soft int64  `json:"Soft" tfsdk:"soft"`
	Hard int64  `json:"Hard" tfsdk:"hard"`
}
