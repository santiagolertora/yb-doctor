package fixture

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func TestExpandBalancedAndFaults(t *testing.T) {
	t.Parallel()
	plan := Plan{
		ReplicationFactor: 3,
		TServers: []domain.TServer{
			{ID: "n1", Name: "yb-1", Status: domain.StatusAlive},
			{ID: "n2", Name: "yb-2", Status: domain.StatusAlive},
			{ID: "n3", Name: "yb-3", Status: domain.StatusAlive},
		},
		SlowFollowers:   2,
		HotTablets:      1,
		Leaderless:      1,
		UnderReplicated: 1,
	}
	snap, err := Expand(plan)
	require.NoError(t, err)
	require.Len(t, snap.Tablets, 9)
	require.Equal(t, 3, snap.ReplicationFactor)
	require.Equal(t, 5000.0, snap.Tablets[0].WriteOps)
	require.False(t, snap.Tablets[0].HasLeader())
}

func TestCollectorReadsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `{
  "replication_factor": 3,
  "tservers": [
    {"id": "n1", "name": "yb-1", "status": "ALIVE"},
    {"id": "n2", "name": "yb-2", "status": "ALIVE"},
    {"id": "n3", "name": "yb-3", "status": "ALIVE"}
  ],
  "leaders": {"yb-1": 2, "yb-2": 2, "yb-3": 2},
  "followers": {"yb-1": 4, "yb-2": 4, "yb-3": 4}
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scenario.json"), []byte(body), 0o600))
	c, err := New(dir)
	require.NoError(t, err)
	snap, err := c.Collect(t.Context())
	require.NoError(t, err)
	require.Len(t, snap.Tablets, 6)
}

func TestNewEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := New("")
	require.Error(t, err)
}

func TestCollectCanceled(t *testing.T) {
	t.Parallel()
	c, err := New(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = c.Collect(ctx)
	require.Error(t, err)
}

func TestExpandNoTServers(t *testing.T) {
	t.Parallel()
	_, err := Expand(Plan{})
	require.Error(t, err)
}
