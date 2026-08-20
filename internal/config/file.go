package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DefaultFileName is loaded from the working directory when --config and
// YB_DOCTOR_CONFIG are unset and the file exists.
const DefaultFileName = "yb-doctor.toml"

type fileConfig struct {
	Masters     []string  `toml:"masters"`
	Scenario    string    `toml:"scenario"`
	Format      string    `toml:"format"`
	LogLevel    string    `toml:"log_level"`
	NoColor     bool      `toml:"no_color"`
	HTTPTimeout string    `toml:"http_timeout"`
	TLSCAFile   string    `toml:"tls_ca_file"`
	TLSSkip     bool      `toml:"tls_skip_verify"`
	Criteria    string    `toml:"criteria"`
	TServerBase int       `toml:"tserver_http_base"`
	YSQL        fileYSQL  `toml:"ysql"`
	Watch       fileWatch `toml:"watch"`
}

type fileYSQL struct {
	Addr     string `toml:"addr"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	Database string `toml:"database"`
	SSLMode  string `toml:"sslmode"`
	Timeout  string `toml:"timeout"`
}

type fileWatch struct {
	Duration string `toml:"duration"`
	Interval string `toml:"interval"`
}

// ResolveFile returns the config path. Flag wins, then YB_DOCTOR_CONFIG, then
// ./yb-doctor.toml if it exists.
func ResolveFile(flagPath string, getenv func(string) string) string {
	if flagPath != "" {
		return flagPath
	}
	if getenv != nil {
		if v := getenv("YB_DOCTOR_CONFIG"); v != "" {
			return v
		}
	}
	if st, err := os.Stat(DefaultFileName); err == nil && !st.IsDir() {
		return DefaultFileName
	}
	return ""
}

// WithFile applies a TOML config file. Empty path is a no-op.
func WithFile(path string) Source {
	return func(cfg *Config) error {
		if path == "" {
			return nil
		}
		raw, err := os.ReadFile(path) //nolint:gosec // path is --config / YB_DOCTOR_CONFIG / cwd
		if err != nil {
			return fmt.Errorf("read config file: %w", err)
		}
		var fc fileConfig
		if err := toml.Unmarshal(raw, &fc); err != nil {
			return fmt.Errorf("parse config file: %w", err)
		}
		return applyFile(cfg, fc)
	}
}

func applyFile(cfg *Config, fc fileConfig) error {
	if len(fc.Masters) > 0 {
		cfg.Masters = append([]string{}, fc.Masters...)
	}
	if fc.Scenario != "" {
		cfg.Scenario = fc.Scenario
	}
	if fc.Format != "" {
		cfg.Format = fc.Format
	}
	if fc.LogLevel != "" {
		cfg.LogLevel = fc.LogLevel
	}
	if fc.NoColor {
		cfg.NoColor = true
	}
	if fc.HTTPTimeout != "" {
		d, err := time.ParseDuration(fc.HTTPTimeout)
		if err != nil {
			return fmt.Errorf("config file http_timeout: %w", err)
		}
		cfg.HTTPTimeout = d
	}
	if fc.TLSCAFile != "" {
		cfg.TLSCAFile = fc.TLSCAFile
	}
	if fc.TLSSkip {
		cfg.TLSSkipVerify = true
	}
	if fc.Criteria != "" {
		cfg.CriteriaFile = fc.Criteria
	}
	if fc.TServerBase > 0 {
		cfg.LoopbackTServerPortBase = fc.TServerBase
	}
	if fc.YSQL.Addr != "" {
		cfg.YSQLAddr = fc.YSQL.Addr
	}
	if fc.YSQL.User != "" {
		cfg.YSQLUser = fc.YSQL.User
	}
	if fc.YSQL.Password != "" {
		cfg.YSQLPassword = fc.YSQL.Password
	}
	if fc.YSQL.Database != "" {
		cfg.YSQLDatabase = fc.YSQL.Database
	}
	if fc.YSQL.SSLMode != "" {
		cfg.YSQLSSLMode = fc.YSQL.SSLMode
	}
	if fc.YSQL.Timeout != "" {
		d, err := time.ParseDuration(fc.YSQL.Timeout)
		if err != nil {
			return fmt.Errorf("config file ysql.timeout: %w", err)
		}
		cfg.YSQLTimeout = d
	}
	if fc.Watch.Duration != "" {
		d, err := time.ParseDuration(fc.Watch.Duration)
		if err != nil {
			return fmt.Errorf("config file watch.duration: %w", err)
		}
		cfg.WatchDuration = d
	}
	if fc.Watch.Interval != "" {
		d, err := time.ParseDuration(fc.Watch.Interval)
		if err != nil {
			return fmt.Errorf("config file watch.interval: %w", err)
		}
		cfg.WatchInterval = d
	}
	return nil
}
