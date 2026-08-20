package app

import (
	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func analyzeRaft(snap *domain.Snapshot, th config.Thresholds) domain.RaftSummary {
	under := countIDsOr(snap.UnderReplicatedIDs, snap.Tablets, func(t domain.Tablet) bool {
		return t.ReplicaCount() > 0 && t.ReplicaCount() < snap.ReplicationFactor && snap.ReplicationFactor > 0
	})
	leaderless := countIDsOr(snap.LeaderlessIDs, snap.Tablets, func(t domain.Tablet) bool {
		return t.ReplicaCount() > 0 && !t.HasLeader()
	})
	slow := 0
	failed := 0
	for _, tab := range snap.Tablets {
		for _, p := range tab.Peers {
			if p.State == "FAILED" || p.State == "TOMBSTONED" {
				failed++
			}
			if p.Role == domain.RoleFollower && th.SlowFollowerLag > 0 && p.Lag >= th.SlowFollowerLag {
				slow++
			}
		}
	}
	return domain.RaftSummary{
		Leaderless:      leaderless,
		UnderReplicated: under,
		SlowFollowers:   slow,
		FailedPeers:     failed,
	}
}
