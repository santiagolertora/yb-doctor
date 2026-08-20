package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// Resilience simulates node, AZ, and region failures against tablet Raft quorum.
func (s *Service) Resilience(ctx context.Context) (*domain.ResilienceReport, error) {
	snap, err := s.collector.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect snapshot: %w", err)
	}
	return simulateResilience(snap), nil
}

func simulateResilience(snap *domain.Snapshot) *domain.ResilienceReport {
	tree := buildTopologyTree(snap)
	sims := make([]domain.FailureSim, 0)

	for _, ts := range snap.TServers {
		lost := map[domain.NodeID]struct{}{ts.ID: {}}
		sims = append(sims, evalFailure(snap, "Lose "+displayName(ts), "node", []string{displayName(ts)}, lost))
	}

	zones := groupTServers(snap, func(ts domain.TServer) string { return ts.Placement.Key() })
	zoneNames := sortedKeys(zones)
	for _, z := range zoneNames {
		lost := idsOf(zones[z])
		label := zoneLabel(zones[z][0].Placement)
		names := namesOf(zones[z])
		sims = append(sims, evalFailure(snap, "Lose "+label, "az", names, lost))
	}

	if len(zoneNames) >= 2 {
		a, b := zoneNames[0], zoneNames[1]
		lost := idsOf(zones[a])
		for id := range idsOf(zones[b]) {
			lost[id] = struct{}{}
		}
		label := zoneLabel(zones[a][0].Placement) + " + " + zoneLabel(zones[b][0].Placement)
		names := append(namesOf(zones[a]), namesOf(zones[b])...)
		sims = append(sims, evalFailure(snap, "Lose "+label, "az-pair", names, lost))
	}

	regions := groupTServers(snap, func(ts domain.TServer) string { return ts.Placement.RegionKey() })
	for _, r := range sortedKeys(regions) {
		lost := idsOf(regions[r])
		label := regions[r][0].Placement.Region
		if label == "" {
			label = r
		}
		sims = append(sims, evalFailure(snap, "Lose "+label, "region", namesOf(regions[r]), lost))
	}

	rec := recommendation(snap, sims)
	return &domain.ResilienceReport{
		Snapshot:       *snap,
		TopologyTree:   tree,
		Simulations:    sims,
		RPO:            "0 for tolerated failures (Raft synchronous replication)",
		Recommendation: rec,
	}
}

func evalFailure(snap *domain.Snapshot, name, kind string, lostNames []string, lost map[domain.NodeID]struct{}) domain.FailureSim {
	quorum := snap.QuorumSize()
	failed := 0
	for _, tab := range snap.Tablets {
		if tab.ReplicaCount() == 0 {
			continue
		}
		remaining := 0
		for _, p := range tab.VotingPeers() {
			if _, gone := lost[p.TServerID]; gone {
				continue
			}
			if ts, ok := snap.TServerByID(p.TServerID); ok && !ts.Alive() {
				continue
			}
			remaining++
		}
		if remaining < quorum {
			failed++
		}
	}
	status := domain.CheckPass
	reason := "Raft majority retained on every tablet"
	if failed > 0 {
		status = domain.CheckFail
		reason = fmt.Sprintf("quorum lost on %d tablet(s)", failed)
	}
	if len(snap.Tablets) == 0 {
		reason = "no tablets to evaluate"
		status = domain.CheckWarn
	}
	return domain.FailureSim{
		Name:          name,
		Kind:          kind,
		Lost:          lostNames,
		Status:        status,
		TabletsFailed: failed,
		Reason:        reason,
	}
}

func buildTopologyTree(snap *domain.Snapshot) []domain.RegionBranch {
	regions := groupTServers(snap, func(ts domain.TServer) string { return ts.Placement.RegionKey() })
	out := make([]domain.RegionBranch, 0, len(regions))
	for _, rk := range sortedKeys(regions) {
		label := regions[rk][0].Placement.Region
		if label == "" {
			label = rk
		}
		zones := groupList(regions[rk], func(ts domain.TServer) string { return ts.Placement.Zone })
		zbranches := make([]domain.AZBranch, 0, len(zones))
		for _, zk := range sortedKeys(zones) {
			zlabel := zk
			if zlabel == "" {
				zlabel = "unspecified"
			}
			zbranches = append(zbranches, domain.AZBranch{Name: zlabel, Nodes: namesOf(zones[zk])})
		}
		out = append(out, domain.RegionBranch{Name: label, Zones: zbranches})
	}
	return out
}

func recommendation(snap *domain.Snapshot, sims []domain.FailureSim) string {
	regionFail := false
	azFail := false
	nodeFail := false
	for _, sim := range sims {
		if sim.Status != domain.CheckFail {
			continue
		}
		switch sim.Kind {
		case "region":
			regionFail = true
		case "az":
			azFail = true
		case "node":
			nodeFail = true
		}
	}
	switch {
	case nodeFail:
		return "A single node loss already breaks Raft majority. Increase RF or add TServers before production."
	case azFail:
		return "An availability-zone loss breaks quorum. Place at most one replica per AZ (RF=3 across 3 AZs)."
	case regionFail && len(snap.Regions()) == 1:
		return "Add cross-region replica placement if regional failure tolerance is required."
	case regionFail:
		return "A regional outage loses quorum. Add a replica in another region or use a read replica / xCluster pattern for DR."
	default:
		return "Current placement tolerates the simulated node and AZ failures."
	}
}

func groupTServers(snap *domain.Snapshot, key func(domain.TServer) string) map[string][]domain.TServer {
	return groupList(snap.TServers, key)
}

func groupList(nodes []domain.TServer, key func(domain.TServer) string) map[string][]domain.TServer {
	out := map[string][]domain.TServer{}
	for _, ts := range nodes {
		k := key(ts)
		out[k] = append(out[k], ts)
	}
	return out
}

func idsOf(nodes []domain.TServer) map[domain.NodeID]struct{} {
	out := make(map[domain.NodeID]struct{}, len(nodes))
	for _, n := range nodes {
		out[n.ID] = struct{}{}
	}
	return out
}

func namesOf(nodes []domain.TServer) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, displayName(n))
	}
	sort.Strings(out)
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func zoneLabel(p domain.Placement) string {
	if p.Zone != "" {
		return p.Zone
	}
	return p.Key()
}
