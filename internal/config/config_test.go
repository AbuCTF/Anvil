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

func TestValidateAllowsDevelopmentDefaultJWTSecret(t *testing.T) {
	cfg := Config{Environment: "development"}
	cfg.JWT.Secret = defaultJWTSecret
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected development JWT secret: %v", err)
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
