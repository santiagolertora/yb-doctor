//go:build e2e

package e2e_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/adapter/inbound/cli"
	"github.com/santiagolertora/yb-doctor/internal/adapter/outbound/yugabyte"
	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/observability"
)

// TestLiveClusterAnalyze talks to a real YugabyteDB Master HTTP API.
// Start docker compose, then: YB_DOCTOR_MASTERS=127.0.0.1:7000 go test -tags=e2e ./test/e2e/...
func TestLiveClusterAnalyze(t *testing.T) {
	masters := os.Getenv("YB_DOCTOR_MASTERS")
	if masters == "" {
		masters = "127.0.0.1:27000"
	}
	cfg := config.Defaults()
	cfg.Masters = []string{masters}
	cfg.LoopbackTServerPortBase = 28000
	logger := observability.NewLogger(os.Stderr, "error")
	client, err := yugabyte.New(cfg, logger)
	require.NoError(t, err)
	snap, err := client.Collect(t.Context())
	if err != nil {
		t.Skip("skipping: no live YugabyteDB Master at " + masters + " (task lab:up). err=" + err.Error())
	}
	require.NotEmpty(t, snap.Masters)
	require.NotEmpty(t, snap.TServers)
	require.NotEmpty(t, snap.Tablets)
	require.GreaterOrEqual(t, snap.ReplicationFactor, 1)

	code := cli.Execute(t.Context(), cli.Deps{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Args:      []string{"--no-color", "--master", masters, "analyze"},
		LookupEnv: os.Getenv,
	})
	require.Equal(t, 0, code)
}
