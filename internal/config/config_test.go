package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeProductionJWTSecrets(t *testing.T) {
	for _, secret := range []string{"", "   ", "change-me", defaultJWTSecret, "short-custom-secret"} {
		cfg := Config{Environment: "production"}
		cfg.JWT.Secret = secret
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() accepted unsafe production JWT secret %q", secret)
		}
	}
}

func TestValidateAllowsConfiguredProductionJWTSecret(t *testing.T) {
	cfg := Config{Environment: "production"}
	cfg.JWT.Secret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected configured production JWT secret: %v", err)
	}
}

func TestValidateProductionVPNEncryptionKey(t *testing.T) {
	cfg := Config{Environment: "production", VPN: VPNConfig{Enabled: true}}
	cfg.JWT.Secret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted production VPN without a stored-secret encryption key")
	}
	cfg.Secrets.EncryptionKey = "abcdef0123456789abcdef0123456789"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected production VPN encryption key: %v", err)
	}
}

func TestValidateAllowsDevelopmentDefaultJWTSecret(t *testing.T) {
	cfg := Config{Environment: "development"}
	cfg.JWT.Secret = defaultJWTSecret
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected development JWT secret: %v", err)
	}
}

func TestValidateVPNRoutingAndPeerControl(t *testing.T) {
	valid := Config{Environment: "development"}
	valid.VPN.AllowedIPs = "10.60.0.0/15, 10.80.0.0/16"
	valid.VPN.PeerControl = "external"
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() rejected valid VPN config: %v", err)
	}

	badRoute := valid
	badRoute.VPN.AllowedIPs = "10.60.0.0/15, not-a-network"
	if err := badRoute.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid VPN route")
	}
	badControl := valid
	badControl.VPN.PeerControl = "nsenter-somewhere"
	if err := badControl.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown VPN peer control mode")
	}
}

func TestValidateContainerHTTPRouting(t *testing.T) {
	valid := Config{Environment: "development"}
	valid.Container.HTTPRouting = true
	valid.Container.HTTPBaseDomain = "instances.demo.example.org"
	valid.Container.HTTPExternalPort = 443
	valid.Container.HTTPExternalScheme = "https"
	valid.Instancer.HMACSecret = "0123456789abcdef0123456789abcdef"
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() rejected valid HTTP routing config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing domain", mutate: func(c *Config) { c.Container.HTTPBaseDomain = "" }},
		{name: "weak identity secret", mutate: func(c *Config) { c.Instancer.HMACSecret = "short" }},
		{name: "invalid port", mutate: func(c *Config) { c.Container.HTTPExternalPort = 0 }},
		{name: "invalid scheme", mutate: func(c *Config) { c.Container.HTTPExternalScheme = "ftp" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid HTTP routing config")
			}
		})
	}
}

func TestValidateContainerTCPRouting(t *testing.T) {
	valid := Config{Environment: "development"}
	valid.Container.TCPRouting = true
	valid.Container.TCPBaseDomain = "instances.demo.example.org"
	valid.Container.TCPPortMin = 30000
	valid.Container.TCPPortMax = 30199
	valid.Instancer.HMACSecret = "0123456789abcdef0123456789abcdef"
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() rejected valid TCP routing config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing domain", mutate: func(c *Config) { c.Container.TCPBaseDomain = "" }},
		{name: "weak identity secret", mutate: func(c *Config) { c.Instancer.HMACSecret = "short" }},
		{name: "privileged minimum", mutate: func(c *Config) { c.Container.TCPPortMin = 1023 }},
		{name: "minimum above maximum", mutate: func(c *Config) { c.Container.TCPPortMin = 30200 }},
		{name: "maximum above port range", mutate: func(c *Config) { c.Container.TCPPortMax = 65536 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid TCP routing config")
			}
		})
	}
}

func TestValidateSwarmRouting(t *testing.T) {
	valid := Config{Environment: "development"}
	valid.Container.Orchestrator = "swarm"
	valid.Container.HTTPRouting = true
	valid.Container.HTTPBaseDomain = "instances.demo.example.org"
	valid.Container.HTTPExternalPort = 443
	valid.Container.HTTPExternalScheme = "https"
	valid.Container.HTTPRoutesPath = "/var/lib/anvil/traefik-dynamic"
	valid.Container.SwarmHTTPPortMin = 31000
	valid.Container.SwarmHTTPPortMax = 31999
	valid.Container.SwarmNetworkPool = "10.231.0.0/16"
	valid.Container.SwarmNetworkPrefix = 24
	valid.Instancer.HMACSecret = "0123456789abcdef0123456789abcdef"
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() rejected valid Swarm routing config: %v", err)
	}

	for _, mutate := range []func(*Config){
		func(c *Config) { c.Container.Orchestrator = "nomad" },
		func(c *Config) { c.Container.HTTPRoutesPath = "" },
		func(c *Config) { c.Container.SwarmHTTPPortMin = 80 },
		func(c *Config) { c.Container.SwarmHTTPPortMax = 30000 },
		func(c *Config) { c.Container.SwarmNetworkPool = "not-a-prefix" },
		func(c *Config) { c.Container.SwarmNetworkPrefix = 8 },
	} {
		cfg := valid
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate() accepted invalid Swarm routing config")
		}
	}

	overlap := valid
	overlap.Container.TCPRouting = true
	overlap.Container.TCPBaseDomain = valid.Container.HTTPBaseDomain
	overlap.Container.TCPPortMin = 31500
	overlap.Container.TCPPortMax = 32000
	if err := overlap.Validate(); err == nil {
		t.Fatal("Validate() accepted overlapping Swarm HTTP and TCP port ranges")
	}
}

func TestLoadWithoutConfigUsesValidDurations(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if cfg.JWT.RefreshExpiry != 168*time.Hour {
		t.Fatalf("JWT refresh expiry = %s, want 168h", cfg.JWT.RefreshExpiry)
	}
	if cfg.Economy.C2PRate != 0.015 {
		t.Fatalf("economy c2p rate = %v, want validated default 0.015", cfg.Economy.C2PRate)
	}
	if got := cfg.Economy.Grant * cfg.Economy.C2PRate; got != 60 {
		t.Fatalf("full grant settlement = %v, want 60", got)
	}
}

func TestLoadGameCheckerDefaultsAndEnv(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	ck := cfg.Game.Checker
	if ck.TaskTimeout != 30*time.Second || ck.TailMargin != 10*time.Second ||
		ck.CheckedTicks != 0 || !ck.Spread || ck.Concurrency != 64 {
		t.Fatalf("checker defaults = %+v", ck)
	}

	// every key must have a SetDefault or its env var is silently ignored
	t.Setenv("ANVIL_GAME_TICK_INTERVAL", "90s")
	t.Setenv("ANVIL_GAME_CHECKER_TASK_TIMEOUT", "20s")
	t.Setenv("ANVIL_GAME_CHECKER_TAIL_MARGIN", "5s")
	t.Setenv("ANVIL_GAME_CHECKER_CHECKED_TICKS", "4")
	t.Setenv("ANVIL_GAME_CHECKER_SPREAD", "false")
	t.Setenv("ANVIL_GAME_CHECKER_CONCURRENCY", "16")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with env returned an error: %v", err)
	}
	ck = cfg.Game.Checker
	if cfg.Game.TickInterval != 90*time.Second || ck.TaskTimeout != 20*time.Second ||
		ck.TailMargin != 5*time.Second || ck.CheckedTicks != 4 || ck.Spread || ck.Concurrency != 16 {
		t.Fatalf("checker env overrides not applied: tick=%s %+v", cfg.Game.TickInterval, ck)
	}
}

func TestLoadGameMirrorScoringDefaultsAndEnv(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	sc := cfg.Game.Scoring
	if sc.Formula != "legacy" || sc.WeightAD != 0.7 || sc.WeightKoth != 0.3 || sc.WeightJeopardy != 0 ||
		sc.AttackerBonus != 0 || sc.NTeams != 0 || sc.MassFaultTeams != 3 || cfg.Game.InternalToken != "" {
		t.Fatalf("mirror defaults = %+v token=%q", sc, cfg.Game.InternalToken)
	}

	t.Setenv("ANVIL_GAME_SCORING_FORMULA", "mirror")
	t.Setenv("ANVIL_GAME_SCORING_WEIGHT_AD", "0.65")
	t.Setenv("ANVIL_GAME_SCORING_WEIGHT_KOTH", "0.35")
	t.Setenv("ANVIL_GAME_SCORING_WEIGHT_JEOPARDY", "0.15")
	t.Setenv("ANVIL_GAME_SCORING_ATTACKER_BONUS", "1.5")
	t.Setenv("ANVIL_GAME_SCORING_N_TEAMS", "15")
	t.Setenv("ANVIL_GAME_SCORING_MASS_FAULT_TEAMS", "4")
	t.Setenv("ANVIL_GAME_INTERNAL_TOKEN", "s3cret")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with env returned an error: %v", err)
	}
	sc = cfg.Game.Scoring
	if sc.Formula != "mirror" || sc.WeightAD != 0.65 || sc.WeightKoth != 0.35 || sc.WeightJeopardy != 0.15 ||
		sc.AttackerBonus != 1.5 || sc.NTeams != 15 || sc.MassFaultTeams != 4 || cfg.Game.InternalToken != "s3cret" {
		t.Fatalf("mirror env overrides not applied: %+v token=%q", sc, cfg.Game.InternalToken)
	}
}

func TestLoadGameKothFinalsDefaultsAndEnv(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	k := cfg.Game.Koth
	if k.NightRoundInterval != 0 || k.NightStart != "00:00" || k.NightEnd != "06:00" || k.Timezone != "Asia/Kolkata" ||
		k.BountyPerRound != 0 || k.BountyMinStreak != 3 || k.BountyCap != 0 {
		t.Fatalf("koth finals defaults = %+v", k)
	}
	t.Setenv("ANVIL_GAME_KOTH_NIGHT_ROUND_INTERVAL", "30m")
	t.Setenv("ANVIL_GAME_KOTH_NIGHT_START", "00:30")
	t.Setenv("ANVIL_GAME_KOTH_NIGHT_END", "06:00")
	t.Setenv("ANVIL_GAME_KOTH_TIMEZONE", "UTC")
	t.Setenv("ANVIL_GAME_KOTH_BOUNTY_PER_ROUND", "2")
	t.Setenv("ANVIL_GAME_KOTH_BOUNTY_MIN_STREAK", "4")
	t.Setenv("ANVIL_GAME_KOTH_BOUNTY_CAP", "20")
	if cfg, err = Load(); err != nil {
		t.Fatalf("Load() with env returned an error: %v", err)
	}
	k = cfg.Game.Koth
	if k.NightRoundInterval != 30*time.Minute || k.NightStart != "00:30" || k.NightEnd != "06:00" || k.Timezone != "UTC" ||
		k.BountyPerRound != 2 || k.BountyMinStreak != 4 || k.BountyCap != 20 {
		t.Fatalf("koth finals env overrides not applied: %+v", k)
	}
}

func TestLoadMergesEventOverlay(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	overlay := filepath.Join(dir, "finals.yaml")
	if err := os.WriteFile(overlay, []byte(`
game:
  flag_valid_ticks: 4
  scoring:
    formula: mirror
    koth_rank: [12, 7, 4, 2, 1]
  koth:
    night_round_interval: 30m
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANVIL_CONFIG_OVERLAY", overlay)
	t.Setenv("ANVIL_GAME_FLAG_VALID_TICKS", "6") // env still beats the overlay
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with overlay returned an error: %v", err)
	}
	g := cfg.Game
	if g.Scoring.Formula != "mirror" || len(g.Scoring.KothRank) != 5 || g.Scoring.KothRank[0] != 12 ||
		g.Koth.NightRoundInterval != 30*time.Minute || g.FlagValidTicks != 6 {
		t.Fatalf("overlay not merged: formula=%q rank=%v night=%s valid=%d",
			g.Scoring.Formula, g.Scoring.KothRank, g.Koth.NightRoundInterval, g.FlagValidTicks)
	}
	if g.TickInterval != 2*time.Minute {
		t.Fatalf("keys outside the overlay lost their defaults: tick=%s", g.TickInterval)
	}

	t.Setenv("ANVIL_CONFIG_OVERLAY", filepath.Join(dir, "missing.yaml"))
	if _, err := Load(); err == nil {
		t.Fatal("a missing overlay must fail loudly, not run with defaults")
	}
}

// the finals overlay in the repo must load and pass validation as shipped
func TestFinalsOverlayLoads(t *testing.T) {
	overlay, err := filepath.Abs("../../deployments/finals/game.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("ANVIL_CONFIG_OVERLAY", overlay)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("finals overlay: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("finals overlay does not validate: %v", err)
	}
	g := cfg.Game
	if g.Scoring.Formula != "mirror" || g.FlagValidTicks != 4 || g.Scoring.KothHold != 0 ||
		g.Scoring.NTeams != 15 || g.Scoring.MaxTeamNumber() != 15 || len(g.Scoring.KothRank) != 5 || g.Koth.RoundInterval != 15*time.Minute ||
		g.Koth.NightRoundInterval != 30*time.Minute || g.Koth.NightStart != "00:00" || g.Koth.NightEnd != "06:00" ||
		g.Koth.Timezone != "Asia/Kolkata" || g.Koth.BountyCap != 12 ||
		g.Checker.TaskTimeout > g.TickInterval/4 || g.FlagPrefix != "H7CTF" ||
		cfg.RateLimit.RequestsPerMinute < 10000 || cfg.RateLimit.Login.Requests < 500 {
		t.Fatalf("finals overlay values = %+v", g)
	}
}

func TestDefaultEconomyMatchesVersionedPreset(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	want := builtInEconomyPreset.Parameters
	if !reflect.DeepEqual(cfg.Economy, want) {
		t.Fatalf("default economy no longer matches %s %s\ngot:  %#v\nwant: %#v",
			builtInEconomyPreset.ID, builtInEconomyPreset.Version, cfg.Economy, want)
	}
	descriptor := cfg.EconomyPolicyDescriptor()
	if descriptor.ID != "ledger" || descriptor.Version != "1.7" || descriptor.Name != "Ledger" {
		t.Fatalf("unexpected policy identity: %+v", descriptor)
	}
	if !reflect.DeepEqual(builtInEconomyPreset.DifficultyOrder, []string{"easy", "medium", "hard", "insane"}) {
		t.Fatalf("unexpected difficulty order: %v", builtInEconomyPreset.DifficultyOrder)
	}
	if descriptor.Checksum == "" || descriptor.Checksum == "unavailable" || descriptor.Customized {
		t.Fatalf("unexpected canonical descriptor: %+v", descriptor)
	}
	// A rule change requires a deliberate preset-version bump and checksum update.
	if descriptor.Checksum != "ca2461c0fcbbbf04956cb3d6d1af926c08530031fff4b675f0cd6eefd05c573c" {
		t.Fatalf("unexpected Ledger v1.7 checksum: %s", descriptor.Checksum)
	}
	document := cfg.EconomyPolicyDocument()
	if document.SchemaVersion != 1 || document.ID != descriptor.ID || document.Version != descriptor.Version || document.Name != descriptor.Name {
		t.Fatalf("unexpected policy document identity: %+v", document)
	}
	if document.Checksum != descriptor.Checksum || document.Customized || !reflect.DeepEqual(document.Parameters, cfg.Economy) {
		t.Fatalf("unexpected canonical policy document: %+v", document)
	}
	document.DifficultyOrder[0] = "changed"
	document.Parameters.LaunchCosts[0]++
	if builtInEconomyPreset.DifficultyOrder[0] != "easy" || cfg.Economy.LaunchCosts[0] != want.LaunchCosts[0] {
		t.Fatal("policy document exposed mutable configuration slices")
	}

	customConfig := *cfg
	customConfig.Economy = cloneEconomyConfig(cfg.Economy)
	customConfig.Economy.LaunchCosts[0]++
	custom := customConfig.EconomyPolicyDescriptor()
	if !custom.Customized || custom.Checksum == descriptor.Checksum {
		t.Fatalf("overridden policy was not fingerprinted: canonical=%+v custom=%+v", descriptor, custom)
	}
}

func TestValidateRejectsLedgerSettlementAtOrAboveMedium(t *testing.T) {
	cfg := Config{Environment: "development"}
	cfg.Economy.Grant = 4000
	cfg.Economy.Ceilings = []float64{100, 250, 500, 1000}
	cfg.Economy.P2CBase = 1
	cfg.Economy.C2PRate = 0.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a full-grant settlement worth more than a medium challenge")
	}

	cfg.Economy.C2PRate = 0.015
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected the validated 0.015 settlement rate: %v", err)
	}
}

func TestLoadAcceptsDeploymentEnvironmentAlias(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ANVIL_ENV", "production")
	t.Setenv("ANVIL_JWT_SECRET", "short")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if cfg.Environment != "production" {
		t.Fatalf("environment = %q, want production", cfg.Environment)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a weak secret with ANVIL_ENV=production")
	}
}

func TestLoadEnvironmentOverridesConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
environment: production
server:
  port: 8080
database:
  host: postgres
  port: 5432
jwt:
  secret: from-config
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ANVIL_ENVIRONMENT", "development")
	t.Setenv("ANVIL_SERVER_PORT", "18080")
	t.Setenv("ANVIL_DATABASE_HOST", "127.0.0.1")
	t.Setenv("ANVIL_DATABASE_PORT", "55432")
	t.Setenv("ANVIL_JWT_SECRET", "from-environment")
	t.Setenv("ANVIL_STORAGE_PATH", "/srv/anvil/storage")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if cfg.Environment != "development" {
		t.Errorf("environment = %q, want development", cfg.Environment)
	}
	if cfg.Server.Port != 18080 {
		t.Errorf("server port = %d, want 18080", cfg.Server.Port)
	}
	if cfg.Database.Host != "127.0.0.1" || cfg.Database.Port != 55432 {
		t.Errorf("database address = %s:%d, want 127.0.0.1:55432", cfg.Database.Host, cfg.Database.Port)
	}
	if cfg.JWT.Secret != "from-environment" {
		t.Errorf("JWT secret = %q, want from-environment", cfg.JWT.Secret)
	}
	if cfg.Storage.Path != "/srv/anvil/storage" {
		t.Errorf("storage path = %q, want /srv/anvil/storage", cfg.Storage.Path)
	}
}

func TestLoadGameControllerAndOutbreakDefaults(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	// controller_enabled defaults true; outbreak.enabled defaults false
	if !cfg.Game.ControllerEnabled {
		t.Error("game.controller_enabled default should be true")
	}
	if cfg.Game.Outbreak.Enabled {
		t.Error("game.outbreak.enabled default should be false")
	}

	// verify env vars are honoured (SetDefault must exist for BindEnv to work)
	t.Setenv("ANVIL_GAME_CONTROLLER_ENABLED", "false")
	t.Setenv("ANVIL_GAME_OUTBREAK_ENABLED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() with env returned an error: %v", err)
	}
	if cfg.Game.ControllerEnabled {
		t.Error("ANVIL_GAME_CONTROLLER_ENABLED=false was not applied")
	}
	if !cfg.Game.Outbreak.Enabled {
		t.Error("ANVIL_GAME_OUTBREAK_ENABLED=true was not applied")
	}
}

func TestScoringMaxTeamNumber(t *testing.T) {
	for _, tc := range []struct{ n, want int }{{0, 15}, {15, 15}, {16, 16}, {-1, 15}, {255, 15}} {
		if got := (ScoringConfig{NTeams: tc.n}).MaxTeamNumber(); got != tc.want {
			t.Fatalf("MaxTeamNumber(n_teams=%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}
