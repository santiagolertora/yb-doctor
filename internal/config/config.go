// Package config loads and validates yb-doctor runtime configuration.
package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FormatText and FormatJSON are the supported report encodings.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// Config holds every tunable value for the CLI. Business code must not
// invent ports, timeouts, or thresholds; they live here.
type Config struct {
	Masters                 []string      `json:"masters" env:"YB_DOCTOR_MASTERS"`
	Scenario                string        `json:"scenario" env:"YB_DOCTOR_SCENARIO"`
	Format                  string        `json:"format" env:"YB_DOCTOR_FORMAT"`
	LogLevel                string        `json:"log_level" env:"YB_DOCTOR_LOG_LEVEL"`
	NoColor                 bool          `json:"no_color" env:"NO_COLOR"`
	HTTPTimeout             time.Duration `json:"http_timeout" env:"YB_DOCTOR_HTTP_TIMEOUT"`
	CollectTimeout          time.Duration `json:"collect_timeout" env:"YB_DOCTOR_COLLECT_TIMEOUT"`
	MaxConcurrency          int           `json:"max_concurrency" env:"YB_DOCTOR_MAX_CONCURRENCY"`
	TLSCAFile               string        `json:"tls_ca_file" env:"YB_DOCTOR_TLS_CA"`
	TLSSkipVerify           bool          `json:"tls_skip_verify" env:"YB_DOCTOR_TLS_SKIP_VERIFY"`
	CriteriaFile            string        `json:"criteria_file" env:"YB_DOCTOR_CRITERIA"`
	LoopbackTServerPortBase int           `json:"loopback_tserver_port_base" env:"YB_DOCTOR_TSERVER_HTTP_BASE"`
	YSQLAddr                string        `json:"ysql_addr" env:"YB_DOCTOR_YSQL"`
	YSQLUser                string        `json:"ysql_user" env:"YB_DOCTOR_YSQL_USER"`
	YSQLPassword            string        `json:"ysql_password" env:"YB_DOCTOR_YSQL_PASSWORD"`
	YSQLDatabase            string        `json:"ysql_database" env:"YB_DOCTOR_YSQL_DB"`
	YSQLSSLMode             string        `json:"ysql_sslmode" env:"YB_DOCTOR_YSQL_SSLMODE"`
	YSQLTimeout             time.Duration `json:"ysql_timeout" env:"YB_DOCTOR_YSQL_TIMEOUT"`
	WatchDuration           time.Duration `json:"watch_duration" env:"YB_DOCTOR_WATCH"`
	WatchInterval           time.Duration `json:"watch_interval" env:"YB_DOCTOR_WATCH_INTERVAL"`
	DiffFile                string        `json:"diff_file" env:"YB_DOCTOR_DIFF"`
	OutFile                 string        `json:"out_file" env:"YB_DOCTOR_OUT"`
	Thresholds              Thresholds    `json:"thresholds"`
	Scoring                 Scoring       `json:"scoring"`
	FlagAllowlist           []FlagSpec    `json:"flag_allowlist"`
}

// FlagTopicCompaction, FlagTopicMemory, FlagTopicSplitting, and FlagTopicStorage
// select which allowlisted TServer flags may be attached to a finding.
const (
	FlagTopicCompaction = "compaction"
	FlagTopicMemory     = "memory"
	FlagTopicSplitting  = "splitting"
	FlagTopicStorage    = "storage"
)

// FlagSpec is one TServer gflag that may appear as finding evidence when it
// differs from Default. Flags at Default are never printed.
type FlagSpec struct {
	Name    string `json:"name"`
	Default string `json:"default"`
	Topic   string `json:"topic"`
}

// Thresholds control when an analyzer emits a finding.
type Thresholds struct {
	LeaderImbalanceRatio float64       `json:"leader_imbalance_ratio"`
	TabletImbalanceRatio float64       `json:"tablet_imbalance_ratio"`
	SlowFollowerLag      time.Duration `json:"slow_follower_lag"`
	DiskWarnPercent      float64       `json:"disk_warn_percent"`
	DiskHighPercent      float64       `json:"disk_high_percent"`
	CompactionHighBytes  int64         `json:"compaction_high_bytes"`
	CompactionSSTRatio   float64       `json:"compaction_sst_ratio"`
	SSTImbalanceRatio    float64       `json:"sst_imbalance_ratio"`
	HotTabletOpsRatio    float64       `json:"hot_tablet_ops_ratio"`
	P99WarnMS            float64       `json:"p99_warn_ms"`
}

// Scoring is the point deduction table used to compute the overall score.
type Scoring struct {
	Start                 int `json:"start"`
	LeaderlessTablet      int `json:"leaderless_tablet"`
	LeaderlessCap         int `json:"leaderless_cap"`
	UnderReplicatedTablet int `json:"under_replicated_tablet"`
	UnderReplicatedCap    int `json:"under_replicated_cap"`
	DeadTServer           int `json:"dead_tserver"`
	DeadMaster            int `json:"dead_master"`
	LeaderImbalance       int `json:"leader_imbalance"`
	TabletImbalance       int `json:"tablet_imbalance"`
	SSTImbalance          int `json:"sst_imbalance"`
	CompactionHigh        int `json:"compaction_high"`
	SlowFollower          int `json:"slow_follower"`
	SlowFollowerCap       int `json:"slow_follower_cap"`
	DiskHigh              int `json:"disk_high"`
	P99Warn               int `json:"p99_warn"`
}

// Source mutates a Config. Sources are applied in order on top of Defaults.
type Source func(*Config) error

// Defaults returns the baseline configuration. All hardcoded tunables live here.
func Defaults() Config {
	return Config{
		Format:                  FormatText,
		LogLevel:                "info",
		HTTPTimeout:             5 * time.Second,
		CollectTimeout:          20 * time.Second,
		MaxConcurrency:          8,
		LoopbackTServerPortBase: 9000,
		YSQLUser:                "yugabyte",
		YSQLDatabase:            "yugabyte",
		YSQLSSLMode:             "disable",
		YSQLTimeout:             15 * time.Second,
		WatchInterval:           3 * time.Second,
		Thresholds: Thresholds{
			LeaderImbalanceRatio: 1.5,
			TabletImbalanceRatio: 1.25,
			SlowFollowerLag:      1000 * time.Millisecond,
			DiskWarnPercent:      75,
			DiskHighPercent:      85,
			CompactionHighBytes:  1 << 30, // 1 GiB pending
			CompactionSSTRatio:   0.10,
			SSTImbalanceRatio:    3,
			HotTabletOpsRatio:    3,
			P99WarnMS:            20,
		},
		FlagAllowlist: defaultFlagAllowlist(),
		Scoring: Scoring{
			Start:                 100,
			LeaderlessTablet:      15,
			LeaderlessCap:         40,
			UnderReplicatedTablet: 5,
			UnderReplicatedCap:    25,
			DeadTServer:           20,
			DeadMaster:            25,
			LeaderImbalance:       8,
			TabletImbalance:       6,
			SSTImbalance:          6,
			CompactionHigh:        12,
			SlowFollower:          0,
			SlowFollowerCap:       8,
			DiskHigh:              10,
			P99Warn:               2,
		},
	}
}

// Load applies sources on top of Defaults. It does not validate; call Validate.
func Load(_ context.Context, sources ...Source) (*Config, error) {
	cfg := Defaults()
	for _, src := range sources {
		if src == nil {
			continue
		}
		if err := src(&cfg); err != nil {
			return nil, fmt.Errorf("load config source: %w", err)
		}
	}
	return &cfg, nil
}

// WithEnv reads well-known environment variables into cfg.
func WithEnv(getenv func(string) string) Source {
	return func(cfg *Config) error {
		if getenv == nil {
			return errors.New("config: getenv is nil")
		}
		if v := getenv("YB_DOCTOR_MASTERS"); v != "" {
			cfg.Masters = splitCSV(v)
		}
		if v := getenv("YB_DOCTOR_SCENARIO"); v != "" {
			cfg.Scenario = v
		}
		if v := getenv("YB_DOCTOR_FORMAT"); v != "" {
			cfg.Format = strings.ToLower(v)
		}
		if v := getenv("YB_DOCTOR_LOG_LEVEL"); v != "" {
			cfg.LogLevel = strings.ToLower(v)
		}
		if v := getenv("NO_COLOR"); v != "" && v != "0" {
			cfg.NoColor = true
		}
		if v := getenv("YB_DOCTOR_HTTP_TIMEOUT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_HTTP_TIMEOUT: %w", err)
			}
			cfg.HTTPTimeout = d
		}
		if v := getenv("YB_DOCTOR_COLLECT_TIMEOUT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_COLLECT_TIMEOUT: %w", err)
			}
			cfg.CollectTimeout = d
		}
		if v := getenv("YB_DOCTOR_MAX_CONCURRENCY"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_MAX_CONCURRENCY: %w", err)
			}
			cfg.MaxConcurrency = n
		}
		if v := getenv("YB_DOCTOR_TLS_CA"); v != "" {
			cfg.TLSCAFile = v
		}
		if v := getenv("YB_DOCTOR_TLS_SKIP_VERIFY"); v != "" && v != "0" {
			cfg.TLSSkipVerify = true
		}
		if v := getenv("YB_DOCTOR_CRITERIA"); v != "" {
			cfg.CriteriaFile = v
		}
		if v := getenv("YB_DOCTOR_TSERVER_HTTP_BASE"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_TSERVER_HTTP_BASE: %w", err)
			}
			cfg.LoopbackTServerPortBase = n
		}
		if v := getenv("YB_DOCTOR_YSQL"); v != "" {
			cfg.YSQLAddr = v
		}
		if v := getenv("YB_DOCTOR_YSQL_USER"); v != "" {
			cfg.YSQLUser = v
		}
		if v := getenv("YB_DOCTOR_YSQL_PASSWORD"); v != "" {
			cfg.YSQLPassword = v
		}
		if v := getenv("YB_DOCTOR_YSQL_DB"); v != "" {
			cfg.YSQLDatabase = v
		}
		if v := getenv("YB_DOCTOR_YSQL_SSLMODE"); v != "" {
			cfg.YSQLSSLMode = v
		}
		if v := getenv("YB_DOCTOR_YSQL_TIMEOUT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_YSQL_TIMEOUT: %w", err)
			}
			cfg.YSQLTimeout = d
		}
		if v := getenv("YB_DOCTOR_WATCH"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_WATCH: %w", err)
			}
			cfg.WatchDuration = d
		}
		if v := getenv("YB_DOCTOR_WATCH_INTERVAL"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("YB_DOCTOR_WATCH_INTERVAL: %w", err)
			}
			cfg.WatchInterval = d
		}
		if v := getenv("YB_DOCTOR_DIFF"); v != "" {
			cfg.DiffFile = v
		}
		if v := getenv("YB_DOCTOR_OUT"); v != "" {
			cfg.OutFile = v
		}
		return nil
	}
}

// Validate returns aggregated configuration errors.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config: nil")
	}
	var errs []error
	if len(c.Masters) == 0 && strings.TrimSpace(c.Scenario) == "" {
		errs = append(errs, errors.New("config: set --master or --scenario"))
	}
	switch strings.ToLower(c.Format) {
	case FormatText, FormatJSON:
	default:
		errs = append(errs, fmt.Errorf("config: unsupported format %q", c.Format))
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("config: unsupported log level %q", c.LogLevel))
	}
	if c.HTTPTimeout <= 0 {
		errs = append(errs, errors.New("config: http_timeout must be > 0"))
	}
	if c.CollectTimeout <= 0 {
		errs = append(errs, errors.New("config: collect_timeout must be > 0"))
	}
	if c.MaxConcurrency < 1 {
		errs = append(errs, errors.New("config: max_concurrency must be >= 1"))
	}
	if c.LoopbackTServerPortBase < 1 {
		errs = append(errs, errors.New("config: loopback_tserver_port_base must be >= 1"))
	}
	if c.YSQLAddr != "" && !strings.Contains(c.YSQLAddr, ":") {
		errs = append(errs, errors.New("config: ysql addr must be host:port"))
	}
	switch strings.ToLower(c.YSQLSSLMode) {
	case "", "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		errs = append(errs, fmt.Errorf("config: unsupported ysql sslmode %q", c.YSQLSSLMode))
	}
	if c.YSQLTimeout < 0 {
		errs = append(errs, errors.New("config: ysql_timeout must be >= 0"))
	}
	if c.WatchDuration < 0 {
		errs = append(errs, errors.New("config: watch_duration must be >= 0"))
	}
	if c.WatchInterval <= 0 {
		errs = append(errs, errors.New("config: watch_interval must be > 0"))
	}
	if c.Thresholds.LeaderImbalanceRatio < 1 {
		errs = append(errs, errors.New("config: leader_imbalance_ratio must be >= 1"))
	}
	if c.Thresholds.TabletImbalanceRatio < 1 {
		errs = append(errs, errors.New("config: tablet_imbalance_ratio must be >= 1"))
	}
	if c.Thresholds.SSTImbalanceRatio < 1 {
		errs = append(errs, errors.New("config: sst_imbalance_ratio must be >= 1"))
	}
	if c.Thresholds.CompactionSSTRatio <= 0 {
		errs = append(errs, errors.New("config: compaction_sst_ratio must be > 0"))
	}
	if c.Scoring.Start <= 0 {
		errs = append(errs, errors.New("config: scoring.start must be > 0"))
	}
	return errors.Join(errs...)
}

func defaultFlagAllowlist() []FlagSpec {
	return []FlagSpec{
		{Name: "rocksdb_compact_flush_rate_limit_bytes_per_sec", Default: "1073741824", Topic: FlagTopicCompaction},
		{Name: "rocksdb_max_background_compactions", Default: "-1", Topic: FlagTopicCompaction},
		{Name: "full_compaction_pool_max_threads", Default: "1", Topic: FlagTopicCompaction},
		{Name: "use_memory_defaults_optimized_for_ysql", Default: "true", Topic: FlagTopicMemory},
		{Name: "default_memory_limit_to_ram_ratio", Default: "0.85", Topic: FlagTopicMemory},
		{Name: "enable_automatic_tablet_splitting", Default: "true", Topic: FlagTopicSplitting},
		{Name: "ysql_enable_packed_row", Default: "true", Topic: FlagTopicStorage},
		{Name: "yb_enable_read_committed_isolation", Default: "true", Topic: FlagTopicStorage},
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
