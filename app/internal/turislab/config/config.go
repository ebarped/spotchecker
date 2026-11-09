// Package config loads the TurisLab Doctor YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		// try duration as integer seconds
		var sec int64
		if err2 := value.Decode(&sec); err2 == nil {
			d.Duration = time.Duration(sec) * time.Second
			return nil
		}
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	Database DatabaseConfig `yaml:"database"`
	Input    InputConfig    `yaml:"input"`
	Scan     ScanConfig     `yaml:"scan"`
	Sources  SourcesConfig  `yaml:"sources"`
	Identity IdentityConfig `yaml:"identity"`
	Scoring  ScoringConfig  `yaml:"scoring"`
	Tiers    TierConfig     `yaml:"tiers"`
	Server   ServerConfig   `yaml:"server"`
	Agent    AgentConfig    `yaml:"agent"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	Logging  LoggingConfig  `yaml:"logging"`
	Legal    LegalConfig    `yaml:"legal"`
}

type DatabaseConfig struct {
	Driver       string   `yaml:"driver"`
	DSN          string   `yaml:"dsn"`
	MaxOpenConns int      `yaml:"max_open_conns"`
	BusyTimeout  Duration `yaml:"busy_timeout"`
	AutoMigrate  bool     `yaml:"auto_migrate"`
}

type InputConfig struct {
	Path             string   `yaml:"path"`
	Format           string   `yaml:"format"`
	GroundTruthPath  string   `yaml:"ground_truth_path"`
	DefaultCountry   string   `yaml:"default_country"`
	ReloadInterval   Duration `yaml:"reload_interval"`
	StrictValidation bool     `yaml:"strict_validation"`
}

type ScanConfig struct {
	Mode                       string   `yaml:"mode"`
	Repeat                     bool     `yaml:"repeat"`
	TriggerInterval            Duration `yaml:"trigger_interval"`
	TargetsPerTick             int      `yaml:"targets_per_tick"`
	Jitter                     float64  `yaml:"jitter"`
	MaxConcurrentSources       int      `yaml:"max_concurrent_sources"`
	BackoffOn429               Duration `yaml:"backoff_on_429"`
	MaxAttempts                int      `yaml:"max_attempts"`
	StaleAfterDays             int      `yaml:"stale_after_days"`
	MaxSignalsPerEstablishment int      `yaml:"max_signals_per_establishment"`
	BudgetEURPerScan           float64  `yaml:"budget_eur_per_scan"`
}

type SourcesConfig struct {
	GoogleMapsScraper GoogleMapsScraperConfig `yaml:"google_maps_scraper"`
	GooglePlaces      GooglePlacesConfig      `yaml:"google_places"`
	OSM               OSMConfig               `yaml:"osm"`
	Overture          OvertureConfig          `yaml:"overture"`
	Website           WebsiteConfig           `yaml:"website"`
	Phone             PhoneConfig             `yaml:"phone"`
	Registry          RegistryConfig          `yaml:"registry"`
	News              NewsConfig              `yaml:"news"`
	Yelp              YelpConfig              `yaml:"yelp"`
	TripAdvisor       TripAdvisorConfig       `yaml:"tripadvisor"`
}

type GoogleMapsScraperConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Provider         string `yaml:"provider"`
	Binary           string `yaml:"binary"`
	TimeoutSeconds   int    `yaml:"timeout_seconds"`
	MaxPlaces        int    `yaml:"max_places"`
	Depth            int    `yaml:"depth"`
	Language         string `yaml:"language"`
	CacheTTLDays     int    `yaml:"cache_ttl_days"`
	IncludeReviews   bool   `yaml:"include_reviews"`
	ProxyURLEnv      string `yaml:"proxy_url_env"`
	FallbackProvider string `yaml:"fallback_provider"`
	Headless         bool   `yaml:"headless"`
}

type GooglePlacesConfig struct {
	Enabled              bool   `yaml:"enabled"`
	APIKeyEnv            string `yaml:"api_key_env"`
	Language             string `yaml:"language"`
	Region               string `yaml:"region"`
	DetailsTTLDays       int    `yaml:"details_ttl_days"`
	IncludeReviews       bool   `yaml:"include_reviews"`
	TimeoutSeconds       int    `yaml:"timeout_seconds"`
	MaxRequestsPerMinute int    `yaml:"max_requests_per_minute"`
	FieldMask            string `yaml:"field_mask"`
}

type OSMConfig struct {
	OverpassURL    string `yaml:"overpass_url"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	CacheTTLDays   int    `yaml:"cache_ttl_days"`
	MaxElements    int    `yaml:"max_elements"`
	UserAgent      string `yaml:"user_agent"`
	Enabled        bool   `yaml:"enabled"`
}

type OvertureConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Release      string `yaml:"release"`
	LocalPath    string `yaml:"local_path"`
	CacheTTLDays int    `yaml:"cache_ttl_days"`
}

type WebsiteConfig struct {
	Enabled         bool     `yaml:"enabled"`
	TimeoutSeconds  int      `yaml:"timeout_seconds"`
	UserAgent       string   `yaml:"user_agent"`
	MaxPages        int      `yaml:"max_pages"`
	FollowRedirects bool     `yaml:"follow_redirects"`
	CacheTTLDays    int      `yaml:"cache_ttl_days"`
	CheckSSL        bool     `yaml:"check_ssl"`
	Keywords        []string `yaml:"keywords"`
}

type PhoneConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Provider    string `yaml:"provider"`
	CallWindow  string `yaml:"call_window"`
	MaxAttempts int    `yaml:"max_attempts"`
	DiscloseAI  bool   `yaml:"disclose_ai"`
	RecordCalls bool   `yaml:"record_calls"`
}

type RegistryConfig struct {
	Enabled        bool   `yaml:"enabled"`
	URL            string `yaml:"url"`
	APIKeyEnv      string `yaml:"api_key_env"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type NewsConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Provider  string   `yaml:"provider"`
	APIKeyEnv string   `yaml:"api_key_env"`
	Feeds     []string `yaml:"feeds"`
	MaxItems  int      `yaml:"max_items"`
}

type YelpConfig struct {
	Enabled        bool   `yaml:"enabled"`
	APIKeyEnv      string `yaml:"api_key_env"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type TripAdvisorConfig struct {
	Enabled        bool   `yaml:"enabled"`
	APIKeyEnv      string `yaml:"api_key_env"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type IdentityConfig struct {
	AutoMatchThreshold float64         `yaml:"auto_match_threshold"`
	ReviewThreshold    float64         `yaml:"review_threshold"`
	GeoRadiusM         int             `yaml:"geo_radius_m"`
	NormalizeUnicode   bool            `yaml:"normalize_unicode"`
	Weights            IdentityWeights `yaml:"weights"`
}

type IdentityWeights struct {
	Name    float64 `yaml:"name"`
	Address float64 `yaml:"address"`
	Geo     float64 `yaml:"geo"`
	Phone   float64 `yaml:"phone"`
	Website float64 `yaml:"website"`
}

type ScoringConfig struct {
	PriorityWeights      PriorityWeights      `yaml:"priority_weights"`
	ClosureFamilyWeights ClosureFamilyWeights `yaml:"closure_family_weights"`
	QualityFamilyWeights QualityFamilyWeights `yaml:"quality_family_weights"`
	ConfidenceFactor     float64              `yaml:"confidence_factor"`
	HysteresisScans      int                  `yaml:"hysteresis_scans"`
	HysteresisWeight     float64              `yaml:"hysteresis_weight"`
	VolatileDelta        float64              `yaml:"volatile_delta"`
	SeasonalAdjustment   bool                 `yaml:"seasonal_adjustment"`
}

type PriorityWeights struct {
	ClosureRisk  float64 `yaml:"closure_risk"`
	QualityRisk  float64 `yaml:"quality_risk"`
	QualityDelta float64 `yaml:"quality_delta"`
	Staleness    float64 `yaml:"staleness"`
	Impact       float64 `yaml:"impact"`
	Complaints   float64 `yaml:"complaints"`
}

type ClosureFamilyWeights struct {
	DirectStatus float64 `yaml:"direct_status"`
	Activity     float64 `yaml:"activity"`
	Context      float64 `yaml:"context"`
	Official     float64 `yaml:"official"`
}

type QualityFamilyWeights struct {
	Reviews   float64 `yaml:"reviews"`
	Official  float64 `yaml:"official"`
	Product   float64 `yaml:"product"`
	News      float64 `yaml:"news"`
	Freshness float64 `yaml:"freshness"`
}

type TierConfig struct {
	P0Min                 float64 `yaml:"p0_min"`
	P1Min                 float64 `yaml:"p1_min"`
	P2Min                 float64 `yaml:"p2_min"`
	MinConfidence         float64 `yaml:"min_confidence"`
	CollectDataConfidence float64 `yaml:"collect_data_confidence"`
}

type ServerConfig struct {
	Addr         string   `yaml:"addr"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
	IdleTimeout  Duration `yaml:"idle_timeout"`
	CORSOrigins  []string `yaml:"cors_origins"`
	StaticDir    string   `yaml:"static_dir"`
}

type AgentConfig struct {
	Enabled     bool              `yaml:"enabled"`
	ReadOnly    bool              `yaml:"read_only"`
	MaxTurns    int               `yaml:"max_turns"`
	Model       AgentModelConfig  `yaml:"model"`
	MCP         []MCPServerConfig `yaml:"mcp"`
	Suggestions []string          `yaml:"suggestions"`
}

type AgentModelConfig struct {
	Provider  string `yaml:"provider"`
	Name      string `yaml:"name"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// MCPServerConfig points the agent at one external MCP server (streamable
// HTTP) whose tools join the chat.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type LoggingConfig struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	RedactPII bool   `yaml:"redact_pii"`
}

type LegalConfig struct {
	ScrapingAck        bool   `yaml:"scraping_ack"`
	DataRetentionDays  int    `yaml:"data_retention_days"`
	GDPRContact        string `yaml:"gdpr_contact"`
	StoreReviewerNames bool   `yaml:"store_reviewer_names"`
}

func Default() Config {
	return Config{
		Database: DatabaseConfig{
			Driver:       "sqlite",
			DSN:          "./turislab.db",
			MaxOpenConns: 5,
			BusyTimeout:  Duration{5 * time.Second},
			AutoMigrate:  true,
		},
		Input: InputConfig{
			Path:             "./base-de-datos.json",
			Format:           "json",
			GroundTruthPath:  "./ground-truth.json",
			DefaultCountry:   "ES",
			StrictValidation: true,
		},
		Scan: ScanConfig{
			Mode:                       "sequential",
			TriggerInterval:            Duration{1 * time.Minute},
			TargetsPerTick:             1,
			Jitter:                     0.1,
			MaxConcurrentSources:       2,
			BackoffOn429:               Duration{10 * time.Minute},
			MaxAttempts:                3,
			StaleAfterDays:             7,
			MaxSignalsPerEstablishment: 40,
		},
		Sources: SourcesConfig{
			GoogleMapsScraper: GoogleMapsScraperConfig{
				Enabled:        true,
				Provider:       "gosom",
				TimeoutSeconds: 90,
				MaxPlaces:      3,
				Depth:          1,
				Language:       "es",
				CacheTTLDays:   7,
				Headless:       true,
			},
			OSM: OSMConfig{
				Enabled:        true,
				OverpassURL:    "https://overpass-api.de/api/interpreter",
				TimeoutSeconds: 30,
				CacheTTLDays:   30,
				MaxElements:    5,
				UserAgent:      "TurisLabDoctor/0.1",
			},
			Website: WebsiteConfig{
				Enabled:         true,
				TimeoutSeconds:  10,
				UserAgent:       "TurisLabDoctor/0.1",
				MaxPages:        3,
				FollowRedirects: true,
				CacheTTLDays:    3,
				CheckSSL:        true,
				Keywords:        []string{"cerrado", "reformas", "nuevo propietario", "traspaso"},
			},
		},
		Tiers: TierConfig{
			P0Min:                 45,
			P1Min:                 35,
			P2Min:                 20,
			MinConfidence:         0.5,
			CollectDataConfidence: 0.35,
		},
		Server: ServerConfig{
			Addr:         ":8090",
			ReadTimeout:  Duration{15 * time.Second},
			WriteTimeout: Duration{30 * time.Second},
			IdleTimeout:  Duration{60 * time.Second},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Logging: LoggingConfig{
			Level:     "info",
			Format:    "text",
			RedactPII: true,
		},
		Legal: LegalConfig{
			ScrapingAck:        true,
			DataRetentionDays:  90,
			StoreReviewerNames: false,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config file: %w", err)
	}

	expanded := expandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return cfg, fmt.Errorf("unmarshal yaml: %w", err)
	}

	cfg.resolvePaths(filepath.Dir(path))

	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// resolvePaths makes explicit relative paths ("./x", "../x") relative to the
// directory holding the config file instead of the process working directory.
// This lets the same config work whether the binary is started from the repo
// root or from app/. Bare names (e.g. "google-maps-scraper") are left alone so
// they can still be resolved through PATH.
func (c *Config) resolvePaths(baseDir string) {
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) || !strings.HasPrefix(p, ".") {
			return p
		}
		return filepath.Join(baseDir, p)
	}

	if !strings.HasPrefix(c.Database.DSN, "file:") && c.Database.DSN != ":memory:" {
		c.Database.DSN = resolve(c.Database.DSN)
	}
	c.Input.Path = resolve(c.Input.Path)
	c.Input.GroundTruthPath = resolve(c.Input.GroundTruthPath)
	c.Sources.GoogleMapsScraper.Binary = resolve(c.Sources.GoogleMapsScraper.Binary)
	c.Sources.Overture.LocalPath = resolve(c.Sources.Overture.LocalPath)
	c.Server.StaticDir = resolve(c.Server.StaticDir)
}

func (c *Config) validate() error {
	if c.Sources.GoogleMapsScraper.Enabled && !c.Legal.ScrapingAck {
		return fmt.Errorf("legal.scraping_ack must be true when google_maps_scraper.enabled is true")
	}
	return nil
}

func expandEnv(s string) string {
	re := regexp.MustCompile(`\${([a-zA-Z_][a-zA-Z0-9_]*)}`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		name := re.FindStringSubmatch(m)[1]
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		return os.Getenv(name)
	})
}

func (c Config) EnabledSources() []string {
	var list []string
	if c.Sources.GoogleMapsScraper.Enabled {
		list = append(list, "google_maps_scraper")
	}
	if c.Sources.GooglePlaces.Enabled {
		list = append(list, "google_places")
	}
	if c.Sources.OSM.Enabled {
		list = append(list, "osm")
	}
	if c.Sources.Overture.Enabled {
		list = append(list, "overture")
	}
	if c.Sources.Website.Enabled {
		list = append(list, "website")
	}
	if c.Sources.Phone.Enabled {
		list = append(list, "phone")
	}
	return list
}
