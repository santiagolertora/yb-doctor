//go:build integration

package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/adapter/inbound/cli"
)

func TestAnalyzeLeaderImbalanceScenario(t *testing.T) {
	t.Parallel()
	dir := scenarioDir(t, "leader-imbalance")
	var out bytes.Buffer
	code := cli.Execute(t.Context(), cli.Deps{
		Stdout:    &out,
		Stderr:    &bytes.Buffer{},
		Args:      []string{"--no-color", "--scenario", dir, "analyze"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Leader imbalance")
	require.Contains(t, out.String(), "yb-01")
	require.Contains(t, out.String(), "Compaction pressure")
}

func TestResilienceHealthyScenario(t *testing.T) {
	t.Parallel()
	dir := scenarioDir(t, "healthy")
	var out bytes.Buffer
	code := cli.Execute(t.Context(), cli.Deps{
		Stdout:    &out,
		Stderr:    &bytes.Buffer{},
		Args:      []string{"--no-color", "--scenario", dir, "resilience"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "FAIL")
	require.Contains(t, out.String(), "cross-region")
}

func scenarioDir(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	dir := filepath.Join(root, "scenarios", name)
	_, err := os.Stat(filepath.Join(dir, "scenario.json"))
	require.NoError(t, err)
	return dir
}
