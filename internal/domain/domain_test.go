package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlacementKeys(t *testing.T) {
	t.Parallel()
	p := Placement{Cloud: "aws", Region: "eu-west-1", Zone: "eu-west-1a"}
	require.Equal(t, "aws.eu-west-1.eu-west-1a", p.String())
	require.Equal(t, "aws.eu-west-1", p.RegionKey())
	require.Equal(t, "unspecified", Placement{}.String())
	require.Equal(t, "unspecified", Placement{}.RegionKey())
}

func TestTabletHasLeaderAndQuorum(t *testing.T) {
	t.Parallel()
	tab := Tablet{
		ID:       "t1",
		LeaderID: "n1",
		Peers: []TabletPeer{
			{TServerID: "n1", Role: RoleLeader},
			{TServerID: "n2", Role: RoleFollower},
			{TServerID: "n3", Role: RoleFollower, State: "TOMBSTONED"},
		},
	}
	require.True(t, tab.HasLeader())
	require.Equal(t, 2, tab.ReplicaCount())
	require.Equal(t, 0.0, tab.Ops())

	leaderless := Tablet{ID: "t2", Peers: []TabletPeer{{TServerID: "n1", Role: RoleFollower}}}
	require.False(t, leaderless.HasLeader())
}

func TestSnapshotHelpers(t *testing.T) {
	t.Parallel()
	s := Snapshot{
		ReplicationFactor: 3,
		Masters: []Master{
			{ID: "m1", Healthy: true},
			{ID: "m2", Healthy: false},
		},
		TServers: []TServer{
			{ID: "n1", Name: "yb-01", Host: "yb-01", Status: StatusAlive, Placement: Placement{Cloud: "aws", Region: "eu-west-1", Zone: "a"}},
			{ID: "n2", Name: "yb-02", Status: StatusDead, Placement: Placement{Cloud: "aws", Region: "eu-west-1", Zone: "b"}},
			{ID: "n3", Name: "yb-03", Status: StatusAlive, Placement: Placement{Cloud: "aws", Region: "eu-west-2", Zone: "a"}},
		},
	}
	h, tot := s.HealthyMasters()
	require.Equal(t, 1, h)
	require.Equal(t, 2, tot)
	h, tot = s.HealthyTServers()
	require.Equal(t, 2, h)
	require.Equal(t, 3, tot)
	require.Equal(t, 2, s.QuorumSize())
	_, ok := s.TServerByID("n1")
	require.True(t, ok)
	_, ok = s.TServerByName("yb-01")
	require.True(t, ok)
	_, ok = s.TServerByID("missing")
	require.False(t, ok)
	require.Len(t, s.Regions(), 2)
	require.Len(t, s.Zones(), 3)
	s.ReplicationFactor = 0
	require.Equal(t, 1, s.QuorumSize())
}

func TestDefaultPOCCriteria(t *testing.T) {
	t.Parallel()
	c := DefaultPOCCriteria()
	require.Equal(t, 3, c.ReplicationFactor)
	require.True(t, c.TolerateAZFailure)
}
