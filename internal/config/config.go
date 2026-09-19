package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Environment string          `mapstructure:"environment"`
	Server      ServerConfig    `mapstructure:"server"`
	Database    DatabaseConfig  `mapstructure:"database"`
	JWT         JWTConfig       `mapstructure:"jwt"`
	Container   ContainerConfig `mapstructure:"container"`
	VPN         VPNConfig       `mapstructure:"vpn"`
	Platform    PlatformConfig  `mapstructure:"platform"`
	RateLimit   RateLimitConfig `mapstructure:"rate_limit"`
	Game        GameConfig      `mapstructure:"game"`
	Storage     StorageConfig   `mapstructure:"storage"`
	SSO         SSOConfig       `mapstructure:"sso"`
	Economy     EconomyConfig   `mapstructure:"economy"`
	ZeroPool    ZeroPoolConfig  `mapstructure:"zeropool"`
	Discord     DiscordConfig   `mapstructure:"discord"`
}

// ledger economy parameters (player 0-1000 anchor, ×10 of
// ledger-sim/reference_config.json). band arrays index by difficulty: 0=easy,
// 1=medium, 2=hard, 3=insane(=the sim's "novel" tier). the live on/off is the
// runtime economy_mode platform setting; these are the validated numbers, guarded
// by invariant #7 at boot (see validate). any change here => ctf26-1 re-sims.
type EconomyConfig struct {
	Grant             float64   `mapstructure:"grant"`
	Ceilings          []float64 `mapstructure:"ceilings"`
	LaunchCosts       []float64 `mapstructure:"launch_costs"`
	CrowdFloors       []float64 `mapstructure:"crowd_floors"`
	CrowdHalflives    []float64 `mapstructure:"crowd_halflives"`
	CleanRefundFrac   float64   `mapstructure:"clean_refund_frac"`
	AbandonRefundFrac float64   `mapstructure:"abandon_refund_frac"`
	WrongSubPenalty   float64   `mapstructure:"wrong_sub_penalty"`
	WrongSubFloor     float64   `mapstructure:"wrong_sub_floor"`
	ConcurrencyCap    int       `mapstructure:"concurrency_cap"`
	MaxExtensions     int       `mapstructure:"max_extensions"`
	ExtCostFracs      []float64 `mapstructure:"ext_cost_fracs"`
	TimerSteps        []float64 `mapstructure:"timer_steps"`  // per-band open-timer length, in steps
	StepMinutes       float64   `mapstructure:"step_minutes"` // minutes per step
	ExtAddStepsFrac   float64   `mapstructure:"ext_add_steps_frac"`
	Bailout           float64   `mapstructure:"bailout"`
	FreeFlagPoints    float64   `mapstructure:"free_flag_points"`
	P2CBlock          float64   `mapstructure:"p2c_block"`
	P2CBase           float64   `mapstructure:"p2c_base"`
	P2CRateDecay      float64   `mapstructure:"p2c_rate_decay"`
	P2CMinRate        float64   `mapstructure:"p2c_min_rate"`
	C2PRate           float64   `mapstructure:"c2p_rate"`
}

// configures the zeropool -> anvil sso handoff (model b). zeropool signs a
// short-lived jwt that anvil verifies and exchanges for an anvil session.
type SSOConfig struct {
	Enabled      bool   `mapstructure:"enabled"`       // off => the /auth/sso endpoint 404s
	SharedSecret string `mapstructure:"shared_secret"` // hs256 secret shared with zeropool
	Issuer       string `mapstructure:"issuer"`        // expected token iss (e.g. "zeropool")
	Audience     string `mapstructure:"audience"`      // expected token aud (e.g. "anvil")
}

// ZeroPoolConfig is the server-to-server link to ZeroPool (the identity store)
// for native walk-in onboarding: Discord provisions via api_key, email walk-ins
// proxy to ZeroPool's public event registration.
type ZeroPoolConfig struct {
	BaseURL   string `mapstructure:"base_url"`
	APIKey    string `mapstructure:"api_key"`
	EventSlug string `mapstructure:"event_slug"`
	// public turnstile site key for the browser-direct email registration widget
	TurnstileSiteKey string `mapstructure:"turnstile_site_key"`
}

// DiscordConfig configures Anvil-side Discord OAuth for instant walk-in login.
type DiscordConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURI  string `mapstructure:"redirect_uri"`
}

const defaultJWTSecret = "change-me-in-production-please"

// rejects configuration that would make a production deployment unsafe to
// start. development keeps permissive defaults for local work.
func (c Config) Validate() error {
	if strings.EqualFold(strings.TrimSpace(c.Environment), "production") {
		secret := strings.TrimSpace(c.JWT.Secret)
		if secret == defaultJWTSecret || len([]byte(secret)) < 32 {
			return fmt.Errorf("jwt.secret must be at least 32 bytes and non-default in production")
		}
	}
	// ledger economy invariant #7 (round-trips lose value) — the only runtime
	// config-load guard per ctf26-1/metrics.py. validated in every environment.
	if c.Economy.P2CBase > 0 && c.Economy.C2PRate > 0 {
		roundtrip := c.Economy.P2CBase * c.Economy.C2PRate
		if roundtrip >= 1.0 {
			return fmt.Errorf("economy invariant #7 violated: roundtrip product %.4f must be < 1", roundtrip)
		}
		if len(c.Economy.Ceilings) > 1 {
			fullGrantToPoints := c.Economy.Grant * c.Economy.C2PRate
			if midTier := c.Economy.Ceilings[1]; fullGrantToPoints >= midTier {
				return fmt.Errorf("economy invariant #7 violated: converting the full grant yields %.1f pts, must be < one mid-tier value %.1f", fullGrantToPoints, midTier)
			}
		}
	}
	return nil
}

type ServerConfig struct {
	Port            int           `mapstructure:"port"`
	Host            string        `mapstructure:"host"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	TrustedProxies  []string      `mapstructure:"trusted_proxies"`
}

type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	SSLMode      string `mapstructure:"ssl_mode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Database, d.SSLMode,
	)
}

type StorageConfig struct {
	Path string `mapstructure:"path"`
}

type JWTConfig struct {
	Secret        string        `mapstructure:"secret"`
	AccessExpiry  time.Duration `mapstructure:"access_expiry"`
	RefreshExpiry time.Duration `mapstructure:"refresh_expiry"`
	Issuer        string        `mapstructure:"issuer"`
}

type ContainerConfig struct {
	NetworkName     string            `mapstructure:"network_name"`
	NetworkSubnet   string            `mapstructure:"network_subnet"`
	DefaultTimeout  time.Duration     `mapstructure:"default_timeout"`
	MaxPerUser      int               `mapstructure:"max_per_user"`
	CleanupInterval time.Duration     `mapstructure:"cleanup_interval"`
	Labels          map[string]string `mapstructure:"labels"`
}

type VPNConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	Interface         string        `mapstructure:"interface"`
	ListenPort        int           `mapstructure:"listen_port"`
	PublicEndpoint    string        `mapstructure:"public_endpoint"`
	PrivateKey        string        `mapstructure:"private_key"`
	PublicKey         string        `mapstructure:"public_key"`
	AddressRange      string        `mapstructure:"address_range"` // e.g., "10.10.0.0/16"
	DNS               string        `mapstructure:"dns"`
	MTU               int           `mapstructure:"mtu"`
	KeepaliveInterval time.Duration `mapstructure:"keepalive_interval"`
	OnlineWindow      time.Duration `mapstructure:"online_window"`
}

type PlatformConfig struct {
	Name        string `mapstructure:"name"`
	Description string `mapstructure:"description"`

	RegistrationMode string `mapstructure:"registration_mode"` // open, invite, token, disabled

	ScoringEnabled    bool `mapstructure:"scoring_enabled"`
	ScoreboardEnabled bool `mapstructure:"scoreboard_enabled"`
	ScoreboardPublic  bool `mapstructure:"scoreboard_public"`
}

type RateLimitConfig struct {
	Enabled           bool `mapstructure:"enabled"`
	RequestsPerMinute int  `mapstructure:"requests_per_minute"`
	BurstSize         int  `mapstructure:"burst_size"`

	Login          RateLimit `mapstructure:"login"`
	FlagSubmission RateLimit `mapstructure:"flag_submission"`
	InstanceStart  RateLimit `mapstructure:"instance_start"`
	VPNConfigGen   RateLimit `mapstructure:"vpn_config_gen"`
}

type RateLimit struct {
	Requests int           `mapstructure:"requests"`
	Window   time.Duration `mapstructure:"window"`
}

// tunes the attack-defense + koth engine. off by default.
type GameConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	TickInterval   time.Duration `mapstructure:"tick_interval"`
	FlagValidTicks int           `mapstructure:"flag_valid_ticks"`
	FlagPrefix     string        `mapstructure:"flag_prefix"`

	Koth    KothConfig    `mapstructure:"koth"`
	Scoring ScoringConfig `mapstructure:"scoring"`
	Webhook WebhookConfig `mapstructure:"webhook"`
}

type KothConfig struct {
	RoundInterval time.Duration `mapstructure:"round_interval"`
	ResetEnabled  bool          `mapstructure:"reset_enabled"`
}

type ScoringConfig struct {
	AttackBase    float64 `mapstructure:"attack_base"`
	DefenseFactor float64 `mapstructure:"defense_factor"`
	SLAPoints     float64 `mapstructure:"sla_points"`
	KothHold      float64 `mapstructure:"koth_hold"`
	KothRank      []int   `mapstructure:"koth_rank"`
}

type WebhookConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
	Secret  string `mapstructure:"secret"`
	ID      string `mapstructure:"id"`
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/anvil")

	v.SetEnvPrefix("ANVIL")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)
	for _, key := range v.AllKeys() {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("error binding environment variable for %s: %w", key, err)
		}
	}
	// anvil_env is the established deployment variable; anvil_environment is
	// retained as the direct mapstructure spelling.
	if err := v.BindEnv("environment", "ANVIL_ENVIRONMENT", "ANVIL_ENV"); err != nil {
		return nil, fmt.Errorf("error binding environment variable for environment: %w", err)
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("environment", "development")

	v.SetDefault("server.port", 8080)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", "30m")
	v.SetDefault("server.write_timeout", "30m")
	v.SetDefault("server.shutdown_timeout", "25s")
	v.SetDefault("server.trusted_proxies", []string{})

	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "anvil")
	v.SetDefault("database.password", "anvil")
	v.SetDefault("database.database", "anvil")
	v.SetDefault("database.ssl_mode", "disable")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)

	v.SetDefault("storage.path", "./data/storage")

	// ledger economy (player 0-1000 anchor, ×10 of ledger-sim/reference_config.json).
	v.SetDefault("economy.grant", 4000.0)
	v.SetDefault("economy.ceilings", []float64{100, 250, 500, 1000})
	v.SetDefault("economy.launch_costs", []float64{50, 100, 200, 250})
	v.SetDefault("economy.crowd_floors", []float64{0.15, 0.15, 0.15, 0.75})
	v.SetDefault("economy.crowd_halflives", []float64{8, 12, 20, 40})
	v.SetDefault("economy.clean_refund_frac", 0.5)
	v.SetDefault("economy.abandon_refund_frac", 0.3)
	v.SetDefault("economy.wrong_sub_penalty", 0.25)
	v.SetDefault("economy.wrong_sub_floor", 0.2)
	v.SetDefault("economy.concurrency_cap", 3)
	v.SetDefault("economy.max_extensions", 2)
	v.SetDefault("economy.ext_cost_fracs", []float64{0.5, 1.0})
	v.SetDefault("economy.timer_steps", []float64{12, 27, 66, 120})
	v.SetDefault("economy.step_minutes", 10.0)
	v.SetDefault("economy.ext_add_steps_frac", 0.5)
	v.SetDefault("economy.bailout", 100.0)
	v.SetDefault("economy.free_flag_points", 10.0)
	v.SetDefault("economy.p2c_block", 50.0)
	v.SetDefault("economy.p2c_base", 1.0)
	v.SetDefault("economy.p2c_rate_decay", 0.7)
	v.SetDefault("economy.p2c_min_rate", 0.05)
	v.SetDefault("economy.c2p_rate", 0.015)

	v.SetDefault("zeropool.base_url", "")
	v.SetDefault("zeropool.api_key", "")
	v.SetDefault("zeropool.event_slug", "h7ctf-2026")
	v.SetDefault("zeropool.turnstile_site_key", "")
	v.SetDefault("discord.enabled", false)
	v.SetDefault("discord.client_id", "")
	v.SetDefault("discord.client_secret", "")
	v.SetDefault("discord.redirect_uri", "")

	v.SetDefault("sso.enabled", false)
	v.SetDefault("sso.shared_secret", "") // must be defaulted so the env-bind loop (allkeys) binds anvil_sso_shared_secret
	v.SetDefault("sso.issuer", "zeropool")
	v.SetDefault("sso.audience", "anvil")

	v.SetDefault("jwt.secret", defaultJWTSecret)
	v.SetDefault("jwt.access_expiry", "15m")
	v.SetDefault("jwt.refresh_expiry", "168h")
	v.SetDefault("jwt.issuer", "anvil")

	v.SetDefault("container.network_name", "anvil-challenges")
	v.SetDefault("container.network_subnet", "172.20.0.0/16")
	v.SetDefault("container.default_timeout", "2h")
	v.SetDefault("container.max_per_user", 2)
	v.SetDefault("container.cleanup_interval", "5m")
	v.SetDefault("container.labels", map[string]string{})

	v.SetDefault("vpn.enabled", true)
	v.SetDefault("vpn.interface", "wg0")
	v.SetDefault("vpn.listen_port", 51820)
	v.SetDefault("vpn.public_endpoint", "")
	v.SetDefault("vpn.private_key", "")
	v.SetDefault("vpn.public_key", "")
	v.SetDefault("vpn.address_range", "10.10.0.0/16")
	v.SetDefault("vpn.dns", "1.1.1.1")
	v.SetDefault("vpn.mtu", 1420)
	v.SetDefault("vpn.keepalive_interval", "8s")
	v.SetDefault("vpn.online_window", "35s")

	v.SetDefault("platform.name", "Anvil")
	v.SetDefault("platform.description", "Forge your skills")
	v.SetDefault("platform.registration_mode", "open")
	v.SetDefault("platform.scoring_enabled", true)
	v.SetDefault("platform.scoreboard_enabled", true)
	v.SetDefault("platform.scoreboard_public", true)

	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.requests_per_minute", 60)
	v.SetDefault("rate_limit.burst_size", 10)
	v.SetDefault("rate_limit.login.requests", 5)
	v.SetDefault("rate_limit.login.window", "15m")
	v.SetDefault("rate_limit.flag_submission.requests", 10)
	v.SetDefault("rate_limit.flag_submission.window", "1m")
	v.SetDefault("rate_limit.instance_start.requests", 3)
	v.SetDefault("rate_limit.instance_start.window", "10m")
	v.SetDefault("rate_limit.vpn_config_gen.requests", 2)
	v.SetDefault("rate_limit.vpn_config_gen.window", "1h")

	v.SetDefault("game.enabled", false)
	v.SetDefault("game.tick_interval", "2m")
	v.SetDefault("game.flag_valid_ticks", 10)
	v.SetDefault("game.flag_prefix", "H7CTF")
	v.SetDefault("game.koth.round_interval", "15m")
	v.SetDefault("game.koth.reset_enabled", true)
	v.SetDefault("game.scoring.attack_base", 100)
	v.SetDefault("game.scoring.defense_factor", 1.0)
	v.SetDefault("game.scoring.sla_points", 10)
	v.SetDefault("game.scoring.koth_hold", 5)
	v.SetDefault("game.scoring.koth_rank", []int{12, 7, 4, 2, 1})
	v.SetDefault("game.webhook.enabled", false)
	v.SetDefault("game.webhook.url", "")
	v.SetDefault("game.webhook.secret", "")
	v.SetDefault("game.webhook.id", "")
}
