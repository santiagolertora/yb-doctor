package app

import (
	"fmt"
	"sort"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// DiffReports compares a previous analyze result to the current one.
func DiffReports(baseline string, prev, curr *domain.HealthReport) *domain.HealthDiff {
	if prev == nil || curr == nil {
		return nil
	}
	added, removed := findingDelta(prev.Findings, curr.Findings)
	return &domain.HealthDiff{
		Baseline:            baseline,
		ScoreFrom:           prev.Score,
		ScoreTo:             curr.Score,
		MastersFrom:         ratio(prev.Topology.MastersHealthy, prev.Topology.MastersTotal),
		MastersTo:           ratio(curr.Topology.MastersHealthy, curr.Topology.MastersTotal),
		TServersFrom:        ratio(prev.Topology.TServersHealthy, prev.Topology.TServersTotal),
		TServersTo:          ratio(curr.Topology.TServersHealthy, curr.Topology.TServersTotal),
		UnderReplicatedFrom: prev.Raft.UnderReplicated,
		UnderReplicatedTo:   curr.Raft.UnderReplicated,
		LeaderlessFrom:      prev.Raft.Leaderless,
		LeaderlessTo:        curr.Raft.Leaderless,
		P99From:             prev.Performance.P99YSQLMS,
		P99To:               curr.Performance.P99YSQLMS,
		FindingsAdded:       added,
		FindingsRemoved:     removed,
		Leaders:             leaderDeltas(prev.Tablets.PerNode, curr.Tablets.PerNode),
	}
}

func ratio(n, d int) string {
	return fmt.Sprintf("%d/%d", n, d)
}

func findingDelta(prev, curr []domain.Finding) (added, removed []domain.FindingCode) {
	p := map[domain.FindingCode]struct{}{}
	c := map[domain.FindingCode]struct{}{}
	for _, f := range prev {
		p[f.Code] = struct{}{}
	}
	for _, f := range curr {
		c[f.Code] = struct{}{}
	}
	for code := range c {
		if _, ok := p[code]; !ok {
			added = append(added, code)
		}
	}
	for code := range p {
		if _, ok := c[code]; !ok {
			removed = append(removed, code)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i] < added[j] })
	sort.Slice(removed, func(i, j int) bool { return removed[i] < removed[j] })
	return added, removed
}

func leaderDeltas(prev, curr []domain.NodeTabletStats) []domain.NodeCountDelta {
	from := map[string]int{}
	for _, n := range prev {
		from[n.Name] = n.Leaders
	}
	names := make([]string, 0, len(curr)+len(prev))
	seen := map[string]struct{}{}
	for _, n := range curr {
		if _, ok := seen[n.Name]; ok {
			continue
		}
		seen[n.Name] = struct{}{}
		names = append(names, n.Name)
	}
	for _, n := range prev {
		if _, ok := seen[n.Name]; ok {
			continue
		}
		seen[n.Name] = struct{}{}
		names = append(names, n.Name)
	}
	sort.Strings(names)
	out := make([]domain.NodeCountDelta, 0)
	currMap := map[string]int{}
	for _, n := range curr {
		currMap[n.Name] = n.Leaders
	}
	for _, name := range names {
		a, b := from[name], currMap[name]
		if a == b {
			continue
		}
		out = append(out, domain.NodeCountDelta{Name: name, From: a, To: b})
	}
	return out
}
