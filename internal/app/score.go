package app

import (
	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func scoreReport(findings []domain.Finding, raft domain.RaftSummary, sc config.Scoring) int {
	score := sc.Start
	deduct := func(n int) {
		score -= n
		if score < 0 {
			score = 0
		}
	}

	leaderlessPts := raft.Leaderless * sc.LeaderlessTablet
	if leaderlessPts > sc.LeaderlessCap {
		leaderlessPts = sc.LeaderlessCap
	}
	deduct(leaderlessPts)

	underPts := raft.UnderReplicated * sc.UnderReplicatedTablet
	if underPts > sc.UnderReplicatedCap {
		underPts = sc.UnderReplicatedCap
	}
	deduct(underPts)

	slowPts := raft.SlowFollowers * sc.SlowFollower
	if sc.SlowFollowerCap > 0 && slowPts > sc.SlowFollowerCap {
		slowPts = sc.SlowFollowerCap
	}
	deduct(slowPts)

	seen := map[domain.FindingCode]struct{}{}
	for _, f := range findings {
		if _, ok := seen[f.Code]; ok && f.Code != domain.CodeCompactionPressure && f.Code != domain.CodeDiskPressure {
			continue
		}
		seen[f.Code] = struct{}{}
		switch f.Code {
		case domain.CodeDeadTServer:
			deduct(sc.DeadTServer)
		case domain.CodeMasterQuorum:
			deduct(sc.DeadMaster)
		case domain.CodeLeaderImbalance:
			deduct(sc.LeaderImbalance)
		case domain.CodeTabletImbalance:
			deduct(sc.TabletImbalance)
		case domain.CodeSSTImbalance:
			deduct(sc.SSTImbalance)
		case domain.CodeCompactionPressure:
			deduct(sc.CompactionHigh)
		case domain.CodeDiskPressure:
			deduct(sc.DiskHigh)
		case domain.CodeP99Latency:
			deduct(sc.P99Warn)
		}
	}
	return score
}
