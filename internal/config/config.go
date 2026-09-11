package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Environment string          `mapstructure:"environment"`
	Server      ServerConfig    `mapstructure:"server"`
	Database    DatabaseConfig  `mapstructure:"database"`
	Redis       RedisConfig     `mapstructure:"redis"`
	JWT         JWTConfig       `mapstructure:"jwt"`
	Container   ContainerConfig `mapstructure:"container"`
	VPN         VPNConfig       `mapstructure:"vpn"`
	Platform    PlatformConfig  `mapstructure:"platform"`
	RateLimit   RateLimitConfig `mapstructure:"rate_limit"`
	Game        GameConfig      `mapstructure:"game"`
}

const defaultJWTSecret = "change-me-in-production-please"

// Validate checks configuration that would make a production deployment
// unsafe to start. Development keeps permissive defaults for local work.
func (c Config) Validate() error {
	if strings.EqualFold(strings.TrimSpace(c.Environment), "production") {
		secret := strings.TrimSpace(c.JWT.Secret)
		if secret == defaultJWTSecret || len([]byte(secret)) < 32 {
			return fmt.Errorf("jwt.secret must be at least 32 bytes and non-default in production")
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

type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

func (r RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

type JWTConfig struct {
	Secret        string        `mapstructure:"secret"`
	AccessExpiry  time.Duration `mapstructure:"access_expiry"`
	RefreshExpiry time.Duration `mapstructure:"refresh_expiry"`
	Issuer        string        `mapstructure:"issuer"`
}

type ContainerConfig struct {
	Runtime             string            `mapstructure:"runtime"` // docker or podman
	NetworkName         string            `mapstructure:"network_name"`
	NetworkSubnet       string            `mapstructure:"network_subnet"`
	DefaultTimeout      time.Duration     `mapstructure:"default_timeout"`
	MaxPerUser          int               `mapstructure:"max_per_user"`
	CleanupInterval     time.Duration     `mapstructure:"cleanup_interval"`
	HealthCheckInterval time.Duration     `mapstructure:"health_check_interval"`
	Labels              map[string]string `mapstructure:"labels"`
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

	RegistrationMode   string `mapstructure:"registration_mode"` // open, invite, token, disabled
	RequireEmailVerify bool   `mapstructure:"require_email_verify"`

	ScoringEnabled    bool   `mapstructure:"scoring_enabled"`
	ScoreboardEnabled bool   `mapstructure:"scoreboard_enabled"`
	ScoreboardPublic  bool   `mapstructure:"scoreboard_public"`
	ScoringMode       string `mapstructure:"scoring_mode"` // static, dynamic, time_decay

	FlagSubmissionEnabled bool `mapstructure:"flag_submission_enabled"`
	HintsEnabled          bool `mapstructure:"hints_enabled"`
	WriteupSubmission     bool `mapstructure:"writeup_submission"`

	DefaultInstanceTimeout time.Duration `mapstructure:"default_instance_timeout"`
	MaxInstanceExtensions  int           `mapstructure:"max_instance_extensions"`
	ExtensionDuration      time.Duration `mapstructure:"extension_duration"`
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

// GameConfig tunes the Attack-Defense + KotH engine. Off by default.
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

// Load reads configuration from file and environment variables
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
	v.SetDefault("server.read_timeout", "15s")
	v.SetDefault("server.write_timeout", "15s")
	v.SetDefault("server.shutdown_timeout", "30s")

	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "anvil")
	v.SetDefault("database.password", "anvil")
	v.SetDefault("database.database", "anvil")
	v.SetDefault("database.ssl_mode", "disable")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)

	v.SetDefault("redis.host", "localhost")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	v.SetDefault("jwt.secret", defaultJWTSecret)
	v.SetDefault("jwt.access_expiry", "15m")
	v.SetDefault("jwt.refresh_expiry", "7d")
	v.SetDefault("jwt.issuer", "anvil")

	v.SetDefault("container.runtime", "docker")
	v.SetDefault("container.network_name", "anvil-challenges")
	v.SetDefault("container.network_subnet", "172.20.0.0/16")
	v.SetDefault("container.default_timeout", "2h")
	v.SetDefault("container.max_per_user", 2)
	v.SetDefault("container.cleanup_interval", "5m")
	v.SetDefault("container.health_check_interval", "30s")

	v.SetDefault("vpn.enabled", true)
	v.SetDefault("vpn.interface", "wg0")
	v.SetDefault("vpn.listen_port", 51820)
	v.SetDefault("vpn.address_range", "10.10.0.0/16")
	v.SetDefault("vpn.dns", "1.1.1.1")
	v.SetDefault("vpn.mtu", 1420)
	v.SetDefault("vpn.keepalive_interval", "8s")
	v.SetDefault("vpn.online_window", "35s")

	v.SetDefault("platform.name", "Anvil")
	v.SetDefault("platform.description", "Forge your skills")
	v.SetDefault("platform.registration_mode", "open")
	v.SetDefault("platform.require_email_verify", false)
	v.SetDefault("platform.scoring_enabled", true)
	v.SetDefault("platform.scoreboard_enabled", true)
	v.SetDefault("platform.scoreboard_public", true)
	v.SetDefault("platform.scoring_mode", "static")
	v.SetDefault("platform.flag_submission_enabled", true)
	v.SetDefault("platform.hints_enabled", true)
	v.SetDefault("platform.writeup_submission", false)
	v.SetDefault("platform.default_instance_timeout", "2h")
	v.SetDefault("platform.max_instance_extensions", 3)
	v.SetDefault("platform.extension_duration", "30m")

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
}
