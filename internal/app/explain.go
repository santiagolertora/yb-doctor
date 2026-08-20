package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// Explain returns the catalog entry for a finding, with live evidence when a snapshot is available.
func (s *Service) Explain(ctx context.Context, code string) (*domain.Explanation, error) {
	normalized := domain.FindingCode(strings.ToLower(strings.TrimSpace(code)))
	article, ok := catalog[normalized]
	if !ok {
		return nil, fmt.Errorf("app: unknown finding %q (try yb-doctor explain --list)", code)
	}
	exp := article
	snap, err := s.collector.Collect(ctx)
	if err != nil {
		s.logger.Debug("explain without snapshot", "err", err)
		exp.CurrentCluster = []string{"no cluster snapshot available"}
		return &exp, nil
	}
	exp.CurrentCluster = currentClusterLines(normalized, s.analyzeSnapshot(snap))
	if len(exp.CurrentCluster) == 0 {
		exp.CurrentCluster = []string{"no matching evidence in the current cluster"}
	}
	return &exp, nil
}

// KnownFindingCodes returns catalog keys in stable order.
func KnownFindingCodes() []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func currentClusterLines(code domain.FindingCode, report *domain.HealthReport) []string {
	switch code {
	case domain.CodeLeaderImbalance:
		lines := make([]string, 0, len(report.Tablets.PerNode)+1)
		for _, n := range report.Tablets.PerNode {
			mark := ""
			if n.Name == report.Tablets.HottestNode && report.Tablets.LeaderImbalance == domain.CheckWarn {
				mark = fmt.Sprintf("  ← %.1fx vs %s", report.Tablets.LeaderRatio, report.Tablets.ColdestNode)
			}
			lines = append(lines, fmt.Sprintf("%-8s %6d leaders%s", n.Name, n.Leaders, mark))
		}
		return lines
	case domain.CodeTabletImbalance:
		lines := make([]string, 0, len(report.Tablets.PerNode))
		avg := 0.0
		if n := len(report.Tablets.PerNode); n > 0 {
			sum := 0
			for _, row := range report.Tablets.PerNode {
				sum += row.Total
			}
			avg = float64(sum) / float64(n)
		}
		for _, n := range report.Tablets.PerNode {
			mark := ""
			if avg > 0 && float64(n.Total) > avg*1.2 {
				pct := (float64(n.Total)/avg - 1) * 100
				mark = fmt.Sprintf("  ← %.0f%% above average", pct)
			}
			lines = append(lines, fmt.Sprintf("%-8s %6d%s", n.Name, n.Total, mark))
		}
		return lines
	case domain.CodeUnderReplicated:
		return []string{fmt.Sprintf("under-replicated tablets: %d", report.Raft.UnderReplicated)}
	case domain.CodeSSTImbalance:
		return []string{fmt.Sprintf("%s vs %s SST ratio %.1fx", report.Tablets.SSTHottestNode, report.Tablets.SSTColdestNode, report.Tablets.SSTRatio)}
	case domain.CodeLeaderless:
		return []string{fmt.Sprintf("leaderless tablets: %d", report.Raft.Leaderless)}
	case domain.CodeCompactionPressure:
		if len(report.Performance.CompactionNodes) == 0 {
			return []string{"no compaction backlog above threshold"}
		}
		return []string{"compaction HIGH on " + joinComma(report.Performance.CompactionNodes)}
	case domain.CodeSingleRegion:
		return []string{"regions: " + joinComma(report.Topology.Regions)}
	default:
		for _, f := range report.Findings {
			if f.Code == code {
				return f.Evidence
			}
		}
		return nil
	}
}

var catalog = map[domain.FindingCode]domain.Explanation{
	domain.CodeLeaderImbalance: {
		Code:         domain.CodeLeaderImbalance,
		What:         "Raft tablet leaders are unevenly distributed across TServers.",
		WhyItMatters: "The leader is the only replica that serves writes (and, by default, reads). Uneven leadership concentrates CPU, WAL, and network on a few nodes.",
		PossibleCauses: []string{
			"recently added or removed TServer",
			"load balancer still running",
			"placement / leader affinity constraints",
			"a TServer was unavailable during balancing",
		},
		NextSteps: []string{
			"Check cluster balancing status on the Master leader",
			"Confirm every TServer is ALIVE",
			"Review leader-affinity / preferred zones",
			"Allow the built-in load balancer to finish before adding more nodes",
		},
	},
	domain.CodeTabletImbalance: {
		Code:         domain.CodeTabletImbalance,
		What:         "Tablet distribution across TServers is uneven.",
		WhyItMatters: "Uneven tablet placement creates CPU, memory, disk, and network hotspots even when leaders look balanced.",
		PossibleCauses: []string{
			"recently added/removed node",
			"rebalance still running",
			"placement constraints",
			"tablet splitting",
			"node unavailable",
		},
		NextSteps: []string{
			"Check cluster balancing status",
			"Check placement configuration",
			"Check failed TServers",
			"Check tablet splitting activity",
		},
	},
	domain.CodeUnderReplicated: {
		Code:         domain.CodeUnderReplicated,
		What:         "One or more tablets have fewer voting replicas than the configured replication factor.",
		WhyItMatters: "Under-replication shrinks the Raft majority. The next failure can make the tablet leaderless.",
		PossibleCauses: []string{
			"TServer down or excluded",
			"disk full / blacklisted node",
			"tablespace placement that does not match live AZs",
			"remote bootstrap still in progress",
		},
		NextSteps: []string{
			"Inspect /api/v1/tablet-under-replication on the Master leader",
			"Restore or replace the missing TServer",
			"Verify placement_blocks vs actual AZs",
			"Watch remote bootstrap progress before forcing copies",
		},
	},
	domain.CodeLeaderless: {
		Code:         domain.CodeLeaderless,
		What:         "Tablets have no Raft leader.",
		WhyItMatters: "A leaderless tablet cannot accept writes. This is an availability incident, not a warning.",
		PossibleCauses: []string{
			"majority of replicas down",
			"clock skew / hybrid-clock issues",
			"network partition between remaining peers",
			"tablet in a transitional state after a node death",
		},
		NextSteps: []string{
			"Inspect /api/v1/tablet-replication for leaderless_tablets",
			"Bring back enough TServers to restore majority",
			"Check clock synchronization (chrony/NTP)",
			"Only then consider yb-admin force_change_leader as a last resort",
		},
	},
	domain.CodeSlowFollowers: {
		Code:         domain.CodeSlowFollowers,
		What:         "Raft followers are lagging the leader WAL.",
		WhyItMatters: "Lagging followers slow commits (for RF) and lengthen recovery if the leader fails.",
		PossibleCauses: []string{
			"disk saturation on the follower",
			"compaction pressure",
			"network congestion",
			"CPU steal / noisy neighbor",
		},
		NextSteps: []string{
			"Compare follower_lag_ms across TServers",
			"Check compaction and disk utilization on lagging nodes",
			"Verify network RTT between AZs",
		},
	},
	domain.CodeCompactionPressure: {
		Code:         domain.CodeCompactionPressure,
		What:         "DocDB (RocksDB) has a large pending compaction backlog on one or more TServers.",
		WhyItMatters: "Compaction debt increases write amplification, read latency, and disk usage. It is a leading indicator of a write stall.",
		PossibleCauses: []string{
			"sustained write burst",
			"disk already near capacity",
			"too many tablets on the node",
			"undersized compaction threads / SSD too slow",
		},
		NextSteps: []string{
			"Read pending/SST on the hot TServer, not pending bytes alone",
			"Reduce incoming write rate if possible",
			"Confirm disk is not the limiter",
			"Review compaction rate-limit flags if they differ from default",
		},
	},
	domain.CodeSSTImbalance: {
		Code:         domain.CodeSSTImbalance,
		What:         "SST file bytes are unevenly distributed across TServers.",
		WhyItMatters: "Tablet counts can look even while one node holds most of the data. That node takes the flushes, compaction, and disk heat.",
		PossibleCauses: []string{
			"recently added TServer still catching up",
			"tablet splitting not keeping up",
			"a few large tablets on one node",
			"a TServer was down during placement",
		},
		NextSteps: []string{
			"Compare SST bytes to tablet counts on the hot vs cold node",
			"Confirm automatic tablet splitting is enabled",
			"Check whether the load balancer is still running",
		},
	},
	domain.CodeHotTablets: {
		Code:         domain.CodeHotTablets,
		What:         "A few tablets receive a disproportionate share of read/write ops.",
		WhyItMatters: "YugabyteDB shards by tablet. A hot tablet is a hot Raft group: one leader, one WAL, one CPU path.",
		PossibleCauses: []string{
			"sequential / low-cardinality primary key",
			"range hotspot (time-series without hash)",
			"uneven hash distribution",
			"a single large tenant or partition",
		},
		NextSteps: []string{
			"Identify the table behind the hot tablet",
			"Consider hash sharding or a better partition key",
			"Check whether tablet splitting is enabled and catching up",
		},
	},
	domain.CodeDeadTServer: {
		Code:         domain.CodeDeadTServer,
		What:         "A YB-TServer has stopped heartbeating to the Master leader.",
		WhyItMatters: "Tablets with a replica on that node become under-replicated. After follower_unavailable_considered_failed_sec, Master will remote-bootstrap a replacement if capacity exists.",
		PossibleCauses: []string{
			"process crash or OOM",
			"host or disk failure",
			"network isolation",
			"deliberate stop for maintenance",
		},
		NextSteps: []string{
			"Confirm the process and host are down",
			"If planned, wait for under-replication to clear after replacement",
			"If unplanned, restore the node or add a new TServer in the same placement",
		},
	},
	domain.CodeMasterQuorum: {
		Code:         domain.CodeMasterQuorum,
		What:         "The YB-Master Raft group does not have a healthy majority.",
		WhyItMatters: "Without Master quorum the control plane cannot place tablets, run the balancer, or accept DDL. The data plane may still serve already-placed tablets.",
		PossibleCauses: []string{
			"two of three masters down",
			"clock / network issues among masters",
			"misconfigured master addresses",
		},
		NextSteps: []string{
			"Restore Master processes until a majority is healthy",
			"Do not change cluster config until quorum is back",
		},
	},
	domain.CodeDiskPressure: {
		Code:         domain.CodeDiskPressure,
		What:         "A TServer's data disk is filling up.",
		WhyItMatters: "YugabyteDB needs headroom for WAL, SST flushes, and compaction. Crossing ~85-90% is a common path to a stuck node.",
		PossibleCauses: []string{
			"compaction backlog",
			"unbounded WAL retention",
			"tablet imbalance onto this node",
			"undersized volume",
		},
		NextSteps: []string{
			"Check disk utilization per data path",
			"Relieve compaction pressure",
			"Add disk or a TServer in the same AZ",
		},
	},
	domain.CodeSingleRegion: {
		Code:         domain.CodeSingleRegion,
		What:         "Every TServer is in a single cloud region.",
		WhyItMatters: "RF=3 across three AZs survives an AZ loss, not a regional loss. That is a product conversation, not a bug.",
		PossibleCauses: []string{
			"POC or single-region production design",
			"latency constraints that keep all writes in one region",
		},
		NextSteps: []string{
			"Confirm RPO/RTO with the customer",
			"If regional DR is required, discuss sync multi-region vs xCluster",
		},
	},
	domain.CodeP99Latency: {
		Code:         domain.CodeP99Latency,
		What:         "YSQL tail latency is above the configured target.",
		WhyItMatters: "Tail latency usually matters more than average TPS in a POC acceptance bar.",
		PossibleCauses: []string{
			"hot tablets or leader imbalance",
			"compaction / disk pressure",
			"undersized nodes",
			"chatty SQL (missing indexes, high row counts)",
		},
		NextSteps: []string{
			"Correlate with hot tablets and compaction findings",
			"Capture pg_stat_statements / yb_terminated_queries",
			"Revisit hardware sizing against the workload",
		},
	},
}
