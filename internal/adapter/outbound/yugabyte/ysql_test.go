package yugabyte

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func TestYSQLDSN(t *testing.T) {
	t.Parallel()
	d := ysqlDSN("127.0.0.1:26433", "yugabyte", "", "yugabyte", "disable")
	require.Contains(t, d, "postgres://yugabyte@127.0.0.1:26433/yugabyte")
	require.Contains(t, d, "sslmode=disable")
	withPass := ysqlDSN("127.0.0.1:5433", "yugabyte", "s3cret", "yugabyte", "disable")
	require.Contains(t, withPass, "s3cret")
}

func TestOverlayYSQLTablets(t *testing.T) {
	t.Parallel()
	snap := &domain.Snapshot{
		TServers: []domain.TServer{
			{ID: "old", Name: "yugabyte-n1", Host: "yugabyte-n1", Status: domain.StatusAlive},
			{ID: "old2", Name: "yugabyte-n2", Host: "yugabyte-n2", Status: domain.StatusAlive},
		},
		Tablets: []domain.Tablet{{ID: "incomplete"}},
	}
	overlayYSQL(snap, []ysqlServerRow{
		{Host: "yugabyte-n1", UUID: "uuid-1", Cloud: "aws", Region: "eu-west-1", Zone: "eu-west-1a"},
		{Host: "yugabyte-n2", UUID: "uuid-2", Cloud: "aws", Region: "eu-west-1", Zone: "eu-west-1b"},
	}, []ysqlTabletRow{{
		TabletID: "tab-1",
		RelName:  "demo_orders",
		Leader:   "yugabyte-n1:5433",
		Replicas: []string{"yugabyte-n1:5433", "yugabyte-n2:5433"},
	}})
	require.Equal(t, domain.NodeID("uuid-1"), snap.TServers[0].ID)
	require.Equal(t, "eu-west-1a", snap.TServers[0].Placement.Zone)
	require.Len(t, snap.Tablets, 1)
	require.Equal(t, domain.TabletID("tab-1"), snap.Tablets[0].ID)
	require.Equal(t, domain.NodeID("uuid-1"), snap.Tablets[0].LeaderID)
	require.Len(t, snap.Tablets[0].Peers, 2)
	require.True(t, snap.Tablets[0].HasLeader())
}

func TestOverlayYSQLServersWithoutTablets(t *testing.T) {
	t.Parallel()
	snap := &domain.Snapshot{
		TServers: []domain.TServer{
			{ID: "old", Name: "yugabyte-n1", Host: "yugabyte-n1", Status: domain.StatusAlive},
		},
		Tablets: []domain.Tablet{{ID: "keep-http"}},
	}
	overlayYSQL(snap, []ysqlServerRow{
		{Host: "yugabyte-n1", UUID: "uuid-1", Cloud: "aws", Region: "eu-west-1", Zone: "eu-west-1a"},
	}, nil)
	require.Equal(t, domain.NodeID("uuid-1"), snap.TServers[0].ID)
	require.Equal(t, "eu-west-1a", snap.TServers[0].Placement.Zone)
	require.Equal(t, domain.TabletID("keep-http"), snap.Tablets[0].ID)
}

type stubRow struct {
	p99  float64
	slow int
}

func (s stubRow) Scan(dest ...any) error {
	*dest[0].(*float64) = s.p99
	*dest[1].(*int) = s.slow
	return nil
}

type stubConn struct {
	row  stubRow
	args []any
}

func (s *stubConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unused")
}

func (s *stubConn) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	s.args = append([]any{}, args...)
	return s.row
}

func TestQueryPgStatP99PassesThreshold(t *testing.T) {
	t.Parallel()
	c := &stubConn{row: stubRow{p99: 1307.2, slow: 12}}
	p99, slow, err := queryPgStatP99(t.Context(), c, 20)
	require.NoError(t, err)
	require.Equal(t, 1307.2, p99)
	require.Equal(t, 12, slow)
	require.Equal(t, []any{20.0}, c.args)
}

func TestWithTimeoutDoesNotCancelSiblings(t *testing.T) {
	t.Parallel()
	parent := t.Context()
	dead, stop := withTimeout(parent, time.Nanosecond)
	<-dead.Done()
	stop()
	live, stopLive := withTimeout(parent, time.Second)
	defer stopLive()
	require.NoError(t, live.Err())
}

func TestNeedsYBServers(t *testing.T) {
	t.Parallel()
	require.True(t, needsYBServers(nil))
	require.True(t, needsYBServers(&domain.Snapshot{}))
	require.True(t, needsYBServers(&domain.Snapshot{
		TServers: []domain.TServer{{Name: "n1"}},
	}))
	require.False(t, needsYBServers(&domain.Snapshot{
		TServers: []domain.TServer{
			{Name: "n1", Placement: domain.Placement{Region: "eu-west-1"}},
			{Name: "n2", Placement: domain.Placement{Region: "eu-west-1"}},
		},
	}))
}
