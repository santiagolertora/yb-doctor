package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithFileYSQLAndMasters(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "yb-doctor.toml")
	body := `
masters = ["127.0.0.1:27000"]
tserver_http_base = 28000

[ysql]
addr = "example.aws.yugabyte.cloud:5433"
user = "admin"
password = "s3cret"
database = "yugabyte"
sslmode = "require"
timeout = "20s"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := Load(t.Context(), WithFile(path))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	require.Equal(t, []string{"127.0.0.1:27000"}, cfg.Masters)
	require.Equal(t, 28000, cfg.LoopbackTServerPortBase)
	require.Equal(t, "example.aws.yugabyte.cloud:5433", cfg.YSQLAddr)
	require.Equal(t, "admin", cfg.YSQLUser)
	require.Equal(t, "s3cret", cfg.YSQLPassword)
	require.Equal(t, "require", cfg.YSQLSSLMode)
	require.Equal(t, 20*time.Second, cfg.YSQLTimeout)
}

func TestFileThenEnvThenFlags(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "yb-doctor.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
masters = ["file:7000"]
[ysql]
user = "from-file"
sslmode = "require"
`), 0o600))
	cfg, err := Load(t.Context(),
		WithFile(path),
		WithEnv(func(k string) string {
			if k == "YB_DOCTOR_YSQL_USER" {
				return "from-env"
			}
			return ""
		}),
		func(c *Config) error {
			c.YSQLUser = "from-flag"
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"file:7000"}, cfg.Masters)
	require.Equal(t, "from-flag", cfg.YSQLUser)
	require.Equal(t, "require", cfg.YSQLSSLMode)
}

func TestWithFileEmptyPathNoop(t *testing.T) {
	t.Parallel()
	cfg, err := Load(t.Context(), WithFile(""), WithEnv(func(k string) string {
		if k == "YB_DOCTOR_SCENARIO" {
			return "scenarios/x"
		}
		return ""
	}))
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
}

func TestWithFileMissing(t *testing.T) {
	t.Parallel()
	_, err := Load(t.Context(), WithFile(filepath.Join(t.TempDir(), "nope.toml")))
	require.Error(t, err)
}

func TestResolveFileOrder(t *testing.T) {
	t.Parallel()
	require.Equal(t, "flag.toml", ResolveFile("flag.toml", func(string) string { return "env.toml" }))
	require.Equal(t, "env.toml", ResolveFile("", func(k string) string {
		if k == "YB_DOCTOR_CONFIG" {
			return "env.toml"
		}
		return ""
	}))
}

func TestBadSSLMode(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Scenario = "x"
	cfg.YSQLSSLMode = "maybe"
	require.ErrorContains(t, cfg.Validate(), "sslmode")
}
