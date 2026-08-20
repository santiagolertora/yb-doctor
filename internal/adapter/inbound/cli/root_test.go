package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionAndExplainList(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Version:   "testdev",
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"version"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "yb-doctor testdev")

	out.Reset()
	code = Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"explain", "--list"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "leader-imbalance")
}

func TestAnalyzeScenario(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	var out, errBuf bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    &errBuf,
		Args:      []string{"--no-color", "--scenario", dir, "analyze"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code, errBuf.String())
	require.Contains(t, out.String(), "YugabyteDB Cluster Health")
	require.Contains(t, out.String(), "Overall score:")
}

func TestResilienceAndPOC(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "resilience"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Failure simulations")

	out.Reset()
	code = Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "poc"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "POC RESULT")
}

func TestExplainWithScenario(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "explain", "leader-imbalance"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "WHAT")
}

func TestAnalyzeDiffAndOut(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	outFile := filepath.Join(t.TempDir(), "before.json")
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "--format", "json", "analyze", "--out", outFile},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.FileExists(t, outFile)

	out.Reset()
	code = Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "analyze", "--diff", outFile},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Changes since")
	require.Contains(t, out.String(), "Score")
}

func TestAnalyzeWatch(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--scenario", dir, "analyze", "--watch", "80ms", "--watch-interval", "15ms"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Overall score:")
	require.Contains(t, out.String(), "Changes since watch start")
}

func TestMissingSourceIsUsage(t *testing.T) {
	t.Parallel()
	var errBuf bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    ioDiscard(),
		Stderr:    &errBuf,
		Args:      []string{"analyze"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 2, code)
	require.Contains(t, errBuf.String(), "error:")
}

func TestExplainRequiresCode(t *testing.T) {
	t.Parallel()
	code := Execute(t.Context(), Deps{
		Stdout:    ioDiscard(),
		Stderr:    ioDiscard(),
		Args:      []string{"explain"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 2, code)
}

func TestAnalyzeConfigFile(t *testing.T) {
	t.Parallel()
	dir := writeMiniScenario(t)
	cfgPath := filepath.Join(t.TempDir(), "yb-doctor.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("scenario = "+quoteTOML(dir)+"\n"), 0o600))
	var out bytes.Buffer
	code := Execute(t.Context(), Deps{
		Stdout:    &out,
		Stderr:    ioDiscard(),
		Args:      []string{"--no-color", "--config", cfgPath, "analyze"},
		LookupEnv: func(string) string { return "" },
	})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Overall score:")
}

func quoteTOML(s string) string {
	return `"` + s + `"`
}

func writeMiniScenario(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `{
  "replication_factor": 3,
  "masters": [
    {"id": "m1", "host": "yb-1", "role": "LEADER", "healthy": true, "placement": {"cloud":"aws","region":"eu-west-1","zone":"a"}},
    {"id": "m2", "host": "yb-2", "role": "FOLLOWER", "healthy": true, "placement": {"cloud":"aws","region":"eu-west-1","zone":"b"}},
    {"id": "m3", "host": "yb-3", "role": "FOLLOWER", "healthy": true, "placement": {"cloud":"aws","region":"eu-west-1","zone":"c"}}
  ],
  "tservers": [
    {"id": "n1", "name": "yb-1", "host": "yb-1", "status": "ALIVE", "placement": {"cloud":"aws","region":"eu-west-1","zone":"a"}},
    {"id": "n2", "name": "yb-2", "host": "yb-2", "status": "ALIVE", "placement": {"cloud":"aws","region":"eu-west-1","zone":"b"}},
    {"id": "n3", "name": "yb-3", "host": "yb-3", "status": "ALIVE", "placement": {"cloud":"aws","region":"eu-west-1","zone":"c"}}
  ],
  "performance": {"p99_ysql_ms": 8, "nodes": {}},
  "workload": {"tps": 1000, "read_pct": 70, "write_pct": 30}
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scenario.json"), []byte(body), 0o600))
	return dir
}

func ioDiscard() *bytes.Buffer {
	return &bytes.Buffer{}
}
