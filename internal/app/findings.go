package app

import (
	"fmt"
	"math"
	"sort"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func analyzePerformance(snap *domain.Snapshot, th config.Thresholds) domain.PerformanceSummary {
	nodes := make([]string, 0)
	pressure := "OK"
	for name, rt := range snap.Performance.Nodes {
		if compactionBacklog(rt, th) {
			nodes = append(nodes, name)
			pressure = "HIGH"
		}
	}
	sort.Strings(nodes)
	if len(nodes) == 1 {
		pressure = fmt.Sprintf("HIGH on %s", nodes[0])
	} else if len(nodes) > 1 {
		pressure = "HIGH"
	}
	return domain.PerformanceSummary{
		P99YSQLMS:          snap.Performance.P99YSQLMS,
		P99Source:          snap.Performance.P99Source,
		SlowQueries:        snap.Performance.SlowQueries,
		HotTablets:         0, // filled by caller via tablet summary
		CompactionPressure: pressure,
		CompactionNodes:    nodes,
	}
}

func compactionBacklog(rt domain.NodeRuntime, th config.Thresholds) bool {
	if rt.PendingCompactionBytes < th.CompactionHighBytes {
		return false
	}
	if rt.SSTFileBytes <= 0 {
		return true
	}
	return float64(rt.PendingCompactionBytes)/float64(rt.SSTFileBytes) >= th.CompactionSSTRatio
}

func collectFindings(
	snap *domain.Snapshot,
	tablets domain.TabletSummary,
	raft domain.RaftSummary,
	cfg config.Config,
) []domain.Finding {
	th := cfg.Thresholds
	findings := make([]domain.Finding, 0)

	dead := make([]string, 0)
	for _, ts := range snap.TServers {
		if !ts.Alive() {
			dead = append(dead, displayName(ts))
		}
	}
	if len(dead) > 0 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityHigh,
			Code:     domain.CodeDeadTServer,
			Title:    "Dead TServer",
			Summary:  "One or more tablet servers are not heartbeating to the Master leader.",
			Evidence: []string{
				fmt.Sprintf("dead TServers: %s", joinComma(dead)),
				"tablets on those nodes are under-replicated until the node returns or is replaced",
			},
		})
	}

	mh, mt := snap.HealthyMasters()
	if mt > 0 && mh < mt/2+1 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityHigh,
			Code:     domain.CodeMasterQuorum,
			Title:    "Master quorum at risk",
			Summary:  "The YB-Master Raft group does not have a majority of healthy members.",
			Evidence: []string{fmt.Sprintf("healthy masters: %d/%d", mh, mt)},
		})
	}

	if raft.Leaderless > 0 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityHigh,
			Code:     domain.CodeLeaderless,
			Title:    "Leaderless tablets",
			Summary:  "Tablets without a Raft leader cannot serve writes and may stall reads.",
			Evidence: []string{fmt.Sprintf("leaderless tablets: %d", raft.Leaderless)},
		})
	}

	if raft.UnderReplicated > 0 {
		sev := domain.SeverityMedium
		if raft.UnderReplicated > 10 || len(dead) > 0 {
			sev = domain.SeverityHigh
		}
		findings = append(findings, domain.Finding{
			Severity: sev,
			Code:     domain.CodeUnderReplicated,
			Title:    "Under-replicated tablets",
			Summary:  "Some tablets have fewer voting replicas than the configured replication factor.",
			Evidence: []string{
				fmt.Sprintf("under-replicated: %d", raft.UnderReplicated),
				fmt.Sprintf("replication factor: %d", snap.ReplicationFactor),
			},
		})
	}

	if tablets.LeaderImbalance == domain.CheckWarn {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityMedium,
			Code:     domain.CodeLeaderImbalance,
			Title:    "Leader imbalance",
			Summary:  "Raft leaders are concentrated on a subset of TServers, creating CPU and WAL hotspots.",
			Evidence: leaderImbalanceEvidence(tablets, snap.LoadBalancer),
			Node:     tablets.HottestNode,
		})
	}

	if tablets.TabletImbalance == domain.CheckWarn {
		hotT, coldT := extremeAliveNames(snap, nodeStats(tablets), false)
		if hotT == "" {
			hotT, coldT = tablets.HottestNode, tablets.ColdestNode
		}
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityMedium,
			Code:     domain.CodeTabletImbalance,
			Title:    "Tablet imbalance",
			Summary:  "Tablet peers are unevenly placed across TServers.",
			Evidence: []string{tabletImbalanceEvidence(hotT, coldT, tablets.TabletRatio)},
			Node:     hotT,
		})
	}

	if tablets.SSTImbalance == domain.CheckWarn {
		msg := fmt.Sprintf("%s has %.1fx more SST bytes than %s", tablets.SSTHottestNode, tablets.SSTRatio, tablets.SSTColdestNode)
		if math.IsInf(tablets.SSTRatio, 1) {
			msg = fmt.Sprintf("%s holds the SST files; %s has none", tablets.SSTHottestNode, tablets.SSTColdestNode)
		}
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityMedium,
			Code:     domain.CodeSSTImbalance,
			Title:    "SST imbalance",
			Summary:  "DocDB SST files are concentrated on a subset of TServers; tablet counts can hide disk heat.",
			Evidence: []string{msg},
			Node:     tablets.SSTHottestNode,
		})
	}

	if tablets.HotTablets > 0 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityMedium,
			Code:     domain.CodeHotTablets,
			Title:    "Hot tablets",
			Summary:  "A small number of tablets absorb a disproportionate share of operations.",
			Evidence: []string{fmt.Sprintf("hot tablets: %d", tablets.HotTablets)},
		})
	}

	if raft.SlowFollowers > 0 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityLow,
			Code:     domain.CodeSlowFollowers,
			Title:    "Slow followers",
			Summary:  "Some Raft followers are lagging the leader WAL.",
			Evidence: []string{fmt.Sprintf("slow followers: %d", raft.SlowFollowers)},
		})
	}

	for name, rt := range snap.Performance.Nodes {
		if compactionBacklog(rt, th) {
			ev := []string{fmt.Sprintf("pending compaction: %s", formatBytesIEC(rt.PendingCompactionBytes))}
			if rt.SSTFileBytes > 0 {
				ev = append(ev,
					fmt.Sprintf("SST files: %s", formatBytesIEC(rt.SSTFileBytes)),
					fmt.Sprintf("pending/SST: %.0f%%", float64(rt.PendingCompactionBytes)/float64(rt.SSTFileBytes)*100),
				)
			} else {
				ev = append(ev, "SST files: n/a (empty node; backlog still counts)")
			}
			if rt.DiskPercent > 0 {
				ev = append(ev, fmt.Sprintf("disk utilization: %.0f%%", rt.DiskPercent))
			}
			if rt.WriteLatencyDeltaPct > 0 {
				ev = append(ev, fmt.Sprintf("write latency correlated +%.0f%%", rt.WriteLatencyDeltaPct))
			}
			findings = append(findings, domain.Finding{
				Severity: domain.SeverityHigh,
				Code:     domain.CodeCompactionPressure,
				Title:    fmt.Sprintf("Compaction pressure on %s", name),
				Summary:  "DocDB (RocksDB) has a large compaction backlog; write amplification and latency will rise.",
				Evidence: ev,
				Node:     name,
			})
			continue
		}
		if rt.DiskPercent >= th.DiskHighPercent {
			findings = append(findings, domain.Finding{
				Severity: domain.SeverityHigh,
				Code:     domain.CodeDiskPressure,
				Title:    fmt.Sprintf("Disk pressure on %s", name),
				Summary:  "Disk utilization is high enough to threaten compaction, WAL growth, and node stability.",
				Evidence: []string{fmt.Sprintf("disk utilization: %.0f%%", rt.DiskPercent)},
				Node:     name,
			})
		}
	}

	if snap.Performance.P99YSQLMS > 0 && snap.Performance.P99YSQLMS > th.P99WarnMS {
		ev := []string{
			fmt.Sprintf("P99 YSQL latency: %.0fms", snap.Performance.P99YSQLMS),
			fmt.Sprintf("threshold: %.0fms", th.P99WarnMS),
		}
		if snap.Performance.P99Source != "" {
			ev = append(ev, "source: "+snap.Performance.P99Source)
		}
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityMedium,
			Code:     domain.CodeP99Latency,
			Title:    "YSQL P99 latency above target",
			Summary:  "Client-facing tail latency exceeds the configured warning threshold.",
			Evidence: ev,
		})
	}

	if len(snap.Regions()) == 1 && len(snap.TServers) > 0 {
		findings = append(findings, domain.Finding{
			Severity: domain.SeverityLow,
			Code:     domain.CodeSingleRegion,
			Title:    "Single-region deployment",
			Summary:  "All TServers sit in one region; a regional outage takes the universe down.",
			Evidence: []string{
				fmt.Sprintf("regions: %s", joinComma(snap.Regions())),
				"RPO is 0 only for failures the placement can tolerate",
			},
		})
	}

	attachFlagEvidence(findings, snap, cfg.FlagAllowlist)
	return findings
}

func nodeStats(sum domain.TabletSummary) map[domain.NodeID]*domain.NodeTabletStats {
	out := make(map[domain.NodeID]*domain.NodeTabletStats, len(sum.PerNode))
	for i := range sum.PerNode {
		st := sum.PerNode[i]
		out[st.NodeID] = &st
	}
	return out
}

func displayName(ts domain.TServer) string {
	if ts.Name != "" {
		return ts.Name
	}
	return ts.Host
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
