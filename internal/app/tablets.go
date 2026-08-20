package app

import (
	"fmt"
	"math"
	"sort"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func analyzeTablets(snap *domain.Snapshot, th config.Thresholds) domain.TabletSummary {
	perNode := make(map[domain.NodeID]*domain.NodeTabletStats, len(snap.TServers))
	order := make([]domain.NodeID, 0, len(snap.TServers))
	for _, ts := range snap.TServers {
		name := ts.Name
		if name == "" {
			name = ts.Host
		}
		perNode[ts.ID] = &domain.NodeTabletStats{Name: name, NodeID: ts.ID}
		order = append(order, ts.ID)
	}

	under := countIDsOr(snap.UnderReplicatedIDs, snap.Tablets, func(t domain.Tablet) bool {
		return t.ReplicaCount() > 0 && t.ReplicaCount() < snap.ReplicationFactor && snap.ReplicationFactor > 0
	})
	leaderless := countIDsOr(snap.LeaderlessIDs, snap.Tablets, func(t domain.Tablet) bool {
		return t.ReplicaCount() > 0 && !t.HasLeader()
	})

	var opsSum float64
	for _, tab := range snap.Tablets {
		opsSum += tab.Ops()
		for _, p := range tab.VotingPeers() {
			st, ok := perNode[p.TServerID]
			if !ok {
				st = &domain.NodeTabletStats{Name: string(p.TServerID), NodeID: p.TServerID}
				perNode[p.TServerID] = st
				order = append(order, p.TServerID)
			}
			st.Total++
			if p.Role == domain.RoleLeader || (tab.LeaderID != "" && p.TServerID == tab.LeaderID) {
				st.Leaders++
			} else {
				st.Followers++
			}
		}
	}

	rows := make([]domain.NodeTabletStats, 0, len(perNode))
	aliveLeaders := make([]int, 0, len(snap.TServers))
	aliveTotals := make([]int, 0, len(snap.TServers))
	for _, id := range order {
		st := perNode[id]
		if st == nil {
			continue
		}
		rows = append(rows, *st)
		if ts, ok := snap.TServerByID(id); ok && ts.Alive() {
			aliveLeaders = append(aliveLeaders, st.Leaders)
			aliveTotals = append(aliveTotals, st.Total)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	for i := range rows {
		if rt, ok := snap.Performance.Nodes[rows[i].Name]; ok {
			rows[i].SSTBytes = rt.SSTFileBytes
			rows[i].PendingCompactionBytes = rt.PendingCompactionBytes
		}
	}

	leaderRatio, _, _ := maxMinRatio(aliveLeaders)
	tabletRatio, _, _ := maxMinRatio(aliveTotals)
	leaderStatus := domain.CheckPass
	if leaderRatio >= th.LeaderImbalanceRatio && len(aliveLeaders) > 1 {
		leaderStatus = domain.CheckWarn
	}
	tabletStatus := domain.CheckPass
	if tabletRatio >= th.TabletImbalanceRatio && len(aliveTotals) > 1 {
		tabletStatus = domain.CheckWarn
	}

	hotName, coldName := extremeAliveNames(snap, perNode, true)
	sstRatio, sstHot, sstCold := sstSkew(snap)
	sstStatus := domain.CheckPass
	if sstRatio >= th.SSTImbalanceRatio && sstHot != sstCold && sstHot != "" {
		sstStatus = domain.CheckWarn
	}

	avgOps := 0.0
	if n := len(snap.Tablets); n > 0 {
		avgOps = opsSum / float64(n)
	}
	hotTablets := 0
	if avgOps > 0 {
		for _, tab := range snap.Tablets {
			if tab.Ops() >= avgOps*th.HotTabletOpsRatio {
				hotTablets++
			}
		}
	}

	return domain.TabletSummary{
		Total:           len(snap.Tablets),
		UnderReplicated: under,
		Leaderless:      leaderless,
		LeaderImbalance: leaderStatus,
		TabletImbalance: tabletStatus,
		HotTablets:      hotTablets,
		PerNode:         rows,
		LeaderRatio:     leaderRatio,
		TabletRatio:     tabletRatio,
		HottestNode:     hotName,
		ColdestNode:     coldName,
		SSTImbalance:    sstStatus,
		SSTRatio:        sstRatio,
		SSTHottestNode:  sstHot,
		SSTColdestNode:  sstCold,
	}
}

func extremeAliveNames(snap *domain.Snapshot, perNode map[domain.NodeID]*domain.NodeTabletStats, leaders bool) (hot, cold string) {
	var maxN, minN int
	first := true
	for _, ts := range snap.TServers {
		if !ts.Alive() {
			continue
		}
		st := perNode[ts.ID]
		if st == nil {
			continue
		}
		n := st.Total
		if leaders {
			n = st.Leaders
		}
		name := st.Name
		if first {
			maxN, minN = n, n
			hot, cold = name, name
			first = false
			continue
		}
		if n > maxN {
			maxN = n
			hot = name
		}
		if n < minN {
			minN = n
			cold = name
		}
	}
	return hot, cold
}

func maxMinRatio(values []int) (ratio float64, maxIdx, minIdx int) {
	if len(values) == 0 {
		return 1, -1, -1
	}
	maxIdx, minIdx = 0, 0
	maxV, minV := values[0], values[0]
	for i, v := range values {
		if v > maxV {
			maxV = v
			maxIdx = i
		}
		if v < minV {
			minV = v
			minIdx = i
		}
	}
	if minV <= 0 {
		if maxV == 0 {
			return 1, maxIdx, minIdx
		}
		return math.Inf(1), maxIdx, minIdx
	}
	return float64(maxV) / float64(minV), maxIdx, minIdx
}

func sstSkew(snap *domain.Snapshot) (ratio float64, hot, cold string) {
	type pair struct {
		name string
		n    int64
	}
	alive := make([]pair, 0, len(snap.TServers))
	for _, ts := range snap.TServers {
		if !ts.Alive() {
			continue
		}
		name := displayName(ts)
		alive = append(alive, pair{name: name, n: snap.Performance.Nodes[name].SSTFileBytes})
	}
	if len(alive) < 2 {
		return 1, "", ""
	}
	maxP, minP := alive[0], alive[0]
	for _, p := range alive[1:] {
		if p.n > maxP.n {
			maxP = p
		}
		if p.n < minP.n {
			minP = p
		}
	}
	if minP.n <= 0 {
		if maxP.n == 0 {
			return 1, maxP.name, minP.name
		}
		return math.Inf(1), maxP.name, minP.name
	}
	return float64(maxP.n) / float64(minP.n), maxP.name, minP.name
}

func countIDsOr(ids []domain.TabletID, tablets []domain.Tablet, pred func(domain.Tablet) bool) int {
	if len(ids) > 0 {
		return len(ids)
	}
	n := 0
	for _, t := range tablets {
		if pred(t) {
			n++
		}
	}
	return n
}

func leaderImbalanceEvidence(sum domain.TabletSummary, lb domain.LoadBalancer) []string {
	if sum.HottestNode == "" || sum.ColdestNode == "" {
		return nil
	}
	msg := fmt.Sprintf("%s has %.1fx more leaders than %s", sum.HottestNode, sum.LeaderRatio, sum.ColdestNode)
	if math.IsInf(sum.LeaderRatio, 1) {
		msg = fmt.Sprintf("%s holds every Raft leader; %s has none", sum.HottestNode, sum.ColdestNode)
	}
	out := []string{msg}
	switch {
	case lb.HasIdle && !lb.Idle:
		out = append(out, "Master load balancer is still running")
	case lb.HasIdle && lb.Idle:
		out = append(out, "Master load balancer is idle; this skew is leftover placement")
	case lb.Known && !lb.Enabled:
		out = append(out, "Master load balancing is disabled; this skew will not self-heal")
	case lb.Known && lb.Enabled:
		out = append(out, "Master load balancing is enabled; idle state is not exposed on this Master")
	default:
		out = append(out, "consider checking leader balancing")
	}
	return out
}

func tabletImbalanceEvidence(hot, cold string, ratio float64) string {
	if math.IsInf(ratio, 1) {
		return fmt.Sprintf("%s hosts every tablet peer; %s has none", hot, cold)
	}
	return fmt.Sprintf("%s hosts %.0f%% more tablet peers than %s", hot, (ratio-1)*100, cold)
}
