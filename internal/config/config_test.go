package config

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultsValidateWithoutSource(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--master or --scenario")
}

func TestLoadEnvAndValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
		assert  func(t *testing.T, cfg *Config)
	}{
		{
			name: "scenario is enough",
			env:  map[string]string{"YB_DOCTOR_SCENARIO": "scenarios/healthy"},
			assert: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.Equal(t, "scenarios/healthy", cfg.Scenario)
				require.Equal(t, FormatText, cfg.Format)
			},
		},
		{
			name: "masters csv and json format",
			env: map[string]string{
				"YB_DOCTOR_MASTERS":   "yb-1:7000, yb-2:7000",
				"YB_DOCTOR_FORMAT":    "JSON",
				"NO_COLOR":            "1",
				"YB_DOCTOR_LOG_LEVEL": "debug",
			},
			assert: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.Equal(t, []string{"yb-1:7000", "yb-2:7000"}, cfg.Masters)
				require.Equal(t, FormatJSON, cfg.Format)
				require.True(t, cfg.NoColor)
				require.Equal(t, "debug", cfg.LogLevel)
			},
		},
		{
			name:    "bad format",
			env:     map[string]string{"YB_DOCTOR_SCENARIO": "x", "YB_DOCTOR_FORMAT": "yaml"},
			wantErr: "unsupported format",
		},
		{
			name:    "bad timeout",
			env:     map[string]string{"YB_DOCTOR_SCENARIO": "x", "YB_DOCTOR_HTTP_TIMEOUT": "nope"},
			wantErr: "YB_DOCTOR_HTTP_TIMEOUT",
		},
		{
			name: "durations and concurrency",
			env: map[string]string{
				"YB_DOCTOR_SCENARIO":        "x",
				"YB_DOCTOR_HTTP_TIMEOUT":    "2s",
				"YB_DOCTOR_COLLECT_TIMEOUT": "15s",
				"YB_DOCTOR_MAX_CONCURRENCY": "4",
				"YB_DOCTOR_TLS_SKIP_VERIFY": "1",
				"YB_DOCTOR_CRITERIA":        "poc.yaml",
			},
			assert: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.Equal(t, 2*time.Second, cfg.HTTPTimeout)
				require.Equal(t, 15*time.Second, cfg.CollectTimeout)
				require.Equal(t, 4, cfg.MaxConcurrency)
				require.True(t, cfg.TLSSkipVerify)
				require.Equal(t, "poc.yaml", cfg.CriteriaFile)
			},
		},
		{
			name: "ysql watch and diff",
			env: map[string]string{
				"YB_DOCTOR_SCENARIO":       "x",
				"YB_DOCTOR_YSQL":           "127.0.0.1:5433",
				"YB_DOCTOR_YSQL_USER":      "yb",
				"YB_DOCTOR_YSQL_DB":        "demo",
				"YB_DOCTOR_WATCH":          "30s",
				"YB_DOCTOR_WATCH_INTERVAL": "3s",
				"YB_DOCTOR_DIFF":           "before.json",
				"YB_DOCTOR_OUT":            "after.json",
			},
			assert: func(t *testing.T, cfg *Config) {
				t.Helper()
				require.Equal(t, "127.0.0.1:5433", cfg.YSQLAddr)
				require.Equal(t, "yb", cfg.YSQLUser)
				require.Equal(t, "demo", cfg.YSQLDatabase)
				require.Equal(t, 30*time.Second, cfg.WatchDuration)
				require.Equal(t, 3*time.Second, cfg.WatchInterval)
				require.Equal(t, "before.json", cfg.DiffFile)
				require.Equal(t, "after.json", cfg.OutFile)
				require.Equal(t, 15*time.Second, cfg.YSQLTimeout)
			},
		},
		{
			name:    "ysql addr missing port",
			env:     map[string]string{"YB_DOCTOR_SCENARIO": "x", "YB_DOCTOR_YSQL": "localhost"},
			wantErr: "ysql addr must be host:port",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load(t.Context(), WithEnv(func(k string) string { return tc.env[k] }))
			if tc.wantErr != "" && err != nil {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			err = cfg.Validate()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.assert != nil {
				tc.assert(t, cfg)
			}
		})
	}
}

func TestWithEnvNilGetenv(t *testing.T) {
	t.Parallel()
	_, err := Load(t.Context(), WithEnv(nil))
	require.Error(t, err)
}

func TestValidateAggregates(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Format = "xml"
	cfg.LogLevel = "trace"
	cfg.HTTPTimeout = 0
	cfg.CollectTimeout = 0
	cfg.MaxConcurrency = 0
	cfg.Thresholds.LeaderImbalanceRatio = 0.5
	cfg.Thresholds.TabletImbalanceRatio = 0.5
	cfg.Scoring.Start = 0
	err := cfg.Validate()
	require.Error(t, err)
	require.GreaterOrEqual(t, len(unwrapJoin(err)), 8)
}

func TestLoadNilSourceSkipped(t *testing.T) {
	t.Parallel()
	cfg, err := Load(t.Context(), nil, WithEnv(func(k string) string {
		if k == "YB_DOCTOR_SCENARIO" {
			return "scenarios/x"
		}
		return ""
	}))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
}

func unwrapJoin(err error) []error {
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		return joined.Unwrap()
	}
	return []error{err}
}
