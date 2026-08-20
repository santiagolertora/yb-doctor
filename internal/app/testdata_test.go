package app

import (
	"context"
	"sort"
	"time"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

type staticCollector struct {
	snap *domain.Snapshot
	err  error
}

func (c staticCollector) Collect(context.Context) (*domain.Snapshot, error) {
	if c.err != nil {
		return nil, c.err
	}
	cp := *c.snap
	return &cp, nil
}

func sixNodeImbalance() *domain.Snapshot {
	az := func(zone string) domain.Placement {
		return domain.Placement{Cloud: "aws", Region: "eu-west-1", Zone: zone}
	}
	nodes := []domain.TServer{
		{ID: "n1", Name: "yb-01", Host: "yb-01", Status: domain.StatusAlive, Placement: az("eu-west-1a")},
		{ID: "n2", Name: "yb-02", Host: "yb-02", Status: domain.StatusAlive, Placement: az("eu-west-1b")},
		{ID: "n3", Name: "yb-03", Host: "yb-03", Status: domain.StatusAlive, Placement: az("eu-west-1c")},
		{ID: "n4", Name: "yb-04", Host: "yb-04", Status: domain.StatusAlive, Placement: az("eu-west-1a")},
		{ID: "n5", Name: "yb-05", Host: "yb-05", Status: domain.StatusAlive, Placement: az("eu-west-1b")},
		{ID: "n6", Name: "yb-06", Host: "yb-06", Status: domain.StatusAlive, Placement: az("eu-west-1c")},
	}
	leaders := map[domain.NodeID]int{"n1": 312, "n2": 305, "n3": 301, "n4": 121, "n5": 119, "n6": 126}
	followers := map[domain.NodeID]int{"n1": 331, "n2": 337, "n3": 342, "n4": 518, "n5": 521, "n6": 519}
	tablets := expandTablets(leaders, followers)
	// mark a handful of slow followers and hot tablets
	for i := 0; i < 7 && i < len(tablets); i++ {
		for j := range tablets[i].Peers {
			if tablets[i].Peers[j].Role == domain.RoleFollower {
				tablets[i].Peers[j].Lag = 1500 * time.Millisecond
				break
			}
		}
	}
	for i := 0; i < 3 && i < len(tablets); i++ {
		tablets[i].WriteOps = 5000
	}
	return &domain.Snapshot{
		CollectedAt:       time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		ReplicationFactor: 3,
		PlacementBlocks:   []domain.Placement{az("eu-west-1a"), az("eu-west-1b"), az("eu-west-1c")},
		Masters: []domain.Master{
			{ID: "m1", Host: "yb-01", Role: domain.RoleLeader, Healthy: true, Placement: az("eu-west-1a")},
			{ID: "m2", Host: "yb-02", Role: domain.RoleFollower, Healthy: true, Placement: az("eu-west-1b")},
			{ID: "m3", Host: "yb-03", Role: domain.RoleFollower, Healthy: true, Placement: az("eu-west-1c")},
		},
		TServers: nodes,
		Tablets:  tablets,
		Performance: domain.Performance{
			P99YSQLMS:   38,
			SlowQueries: 12,
			Nodes: map[string]domain.NodeRuntime{
				"yb-01": {SSTFileBytes: 10 << 30},
				"yb-02": {SSTFileBytes: 10 << 30},
				"yb-03": {
					DiskPercent:            87,
					PendingCompactionBytes: 1 << 31,
					SSTFileBytes:           12 << 30,
					WriteLatencyDeltaPct:   64,
				},
				"yb-04": {SSTFileBytes: 10 << 30},
				"yb-05": {SSTFileBytes: 10 << 30},
				"yb-06": {SSTFileBytes: 10 << 30},
			},
		},
		Workload: domain.Workload{
			DatabaseBytes: 4_200_000_000_000,
			TPS:           38200,
			ReadPct:       72,
			WritePct:      28,
			Connections:   850,
		},
	}
}

func threeAZHealthy() *domain.Snapshot {
	az := func(zone string) domain.Placement {
		return domain.Placement{Cloud: "aws", Region: "eu-west-1", Zone: zone}
	}
	nodes := []domain.TServer{
		{ID: "n1", Name: "yb-1", Host: "yb-1", Status: domain.StatusAlive, Placement: az("eu-west-1a")},
		{ID: "n2", Name: "yb-2", Host: "yb-2", Status: domain.StatusAlive, Placement: az("eu-west-1b")},
		{ID: "n3", Name: "yb-3", Host: "yb-3", Status: domain.StatusAlive, Placement: az("eu-west-1c")},
	}
	ids := []domain.NodeID{"n1", "n2", "n3"}
	tablets := make([]domain.Tablet, 0, 9)
	for i := 0; i < 9; i++ {
		leader := ids[i%3]
		peers := []domain.TabletPeer{
			{TServerID: ids[i%3], Role: domain.RoleLeader},
			{TServerID: ids[(i+1)%3], Role: domain.RoleFollower},
			{TServerID: ids[(i+2)%3], Role: domain.RoleFollower},
		}
		tablets = append(tablets, domain.Tablet{
			ID:       domain.TabletID(itoa(i)),
			LeaderID: leader,
			Peers:    peers,
			ReadOps:  10,
		})
	}
	return &domain.Snapshot{
		ReplicationFactor: 3,
		Masters: []domain.Master{
			{ID: "m1", Host: "yb-1", Role: domain.RoleLeader, Healthy: true, Placement: az("eu-west-1a")},
			{ID: "m2", Host: "yb-2", Role: domain.RoleFollower, Healthy: true, Placement: az("eu-west-1b")},
			{ID: "m3", Host: "yb-3", Role: domain.RoleFollower, Healthy: true, Placement: az("eu-west-1c")},
		},
		TServers: nodes,
		Tablets:  tablets,
		Performance: domain.Performance{
			P99YSQLMS: 8,
			Nodes:     map[string]domain.NodeRuntime{},
		},
		Workload: domain.Workload{TPS: 51000},
	}
}

func expandTablets(leaders, followers map[domain.NodeID]int) []domain.Tablet {
	ids := make([]domain.NodeID, 0, len(leaders))
	for id := range leaders {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	leaderQ := make([]domain.NodeID, 0)
	for _, id := range ids {
		for i := 0; i < leaders[id]; i++ {
			leaderQ = append(leaderQ, id)
		}
	}
	remainF := make(map[domain.NodeID]int, len(followers))
	for id, n := range followers {
		remainF[id] = n
	}
	tablets := make([]domain.Tablet, 0, len(leaderQ))
	for i, leader := range leaderQ {
		picked := []domain.NodeID{leader}
		for len(picked) < 3 {
			var best domain.NodeID
			bestN := -1
			for id, n := range remainF {
				if containsID(picked, id) || n <= 0 {
					continue
				}
				if n > bestN || (n == bestN && id < best) {
					bestN = n
					best = id
				}
			}
			if best == "" {
				for _, id := range ids {
					if !containsID(picked, id) {
						best = id
						break
					}
				}
			}
			if best == "" {
				break
			}
			picked = append(picked, best)
			remainF[best]--
		}
		peers := make([]domain.TabletPeer, 0, len(picked))
		for j, id := range picked {
			role := domain.RoleFollower
			if j == 0 {
				role = domain.RoleLeader
			}
			peers = append(peers, domain.TabletPeer{TServerID: id, Role: role})
		}
		tablets = append(tablets, domain.Tablet{
			ID:       domain.TabletID("t" + itoa(i)),
			LeaderID: leader,
			Peers:    peers,
			ReadOps:  10,
		})
	}
	return tablets
}

func containsID(ids []domain.NodeID, id domain.NodeID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
