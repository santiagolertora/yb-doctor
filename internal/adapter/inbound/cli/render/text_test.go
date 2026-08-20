package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

var update = flag.Bool("update", false, "update golden files")

func TestHealthGolden(t *testing.T) {
	t.Parallel()
	r := &domain.HealthReport{
		Topology: domain.TopologySummary{
			Nodes: 6, MastersHealthy: 3, MastersTotal: 3,
			TServersHealthy: 6, TServersTotal: 6, ReplicationFactor: 3,
			Regions: []string{"aws.eu-west-1"},
		},
		Tablets: domain.TabletSummary{
			Total: 1284, UnderReplicated: 0,
			LeaderImbalance: domain.CheckWarn, TabletImbalance: domain.CheckPass,
			LeaderRatio: 2.6, HottestNode: "yb-01", ColdestNode: "yb-05",
			PerNode: []domain.NodeTabletStats{
				{Name: "yb-01", Leaders: 312, Followers: 331, Total: 643, SSTBytes: 10 << 30},
				{Name: "yb-02", Leaders: 305, Followers: 337, Total: 642, SSTBytes: 10 << 30},
				{Name: "yb-03", Leaders: 301, Followers: 342, Total: 643, SSTBytes: 12 << 30, PendingCompactionBytes: 1 << 31},
				{Name: "yb-04", Leaders: 121, Followers: 518, Total: 639, SSTBytes: 10 << 30},
				{Name: "yb-05", Leaders: 119, Followers: 521, Total: 640, SSTBytes: 10 << 30},
				{Name: "yb-06", Leaders: 126, Followers: 519, Total: 645, SSTBytes: 10 << 30},
			},
		},
		Raft: domain.RaftSummary{SlowFollowers: 7},
		Performance: domain.PerformanceSummary{
			P99YSQLMS: 38, SlowQueries: 12, HotTablets: 3,
			CompactionPressure: "HIGH on yb-03",
		},
		Findings: []domain.Finding{
			{
				Severity: domain.SeverityHigh, Code: domain.CodeCompactionPressure,
				Title: "Compaction pressure on yb-03",
				Evidence: []string{
					"pending compaction: 2.0 GiB",
					"SST files: 12.0 GiB",
					"pending/SST: 17%",
					"disk utilization: 87%",
					"write latency correlated +64%",
				},
			},
			{
				Severity: domain.SeverityMedium, Code: domain.CodeLeaderImbalance,
				Title:    "Leader imbalance",
				Evidence: []string{"consider checking leader balancing"},
			},
		},
		Score: 78,
	}
	var buf bytes.Buffer
	require.NoError(t, Health(&buf, r, Options{NoColor: true}))
	assertGolden(t, "health", buf.Bytes())
}

func TestHealthJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, Health(&buf, &domain.HealthReport{Score: 100}, Options{Format: "json"}))
	require.Contains(t, buf.String(), `"score": 100`)
}

func TestResilienceExplainPOC(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	res := &domain.ResilienceReport{
		Snapshot: domain.Snapshot{ReplicationFactor: 3},
		TopologyTree: []domain.RegionBranch{{
			Name: "eu-west-1",
			Zones: []domain.AZBranch{
				{Name: "AZ-A", Nodes: []string{"yb1"}},
				{Name: "AZ-B", Nodes: []string{"yb2"}},
				{Name: "AZ-C", Nodes: []string{"yb3"}},
			},
		}},
		Simulations: []domain.FailureSim{
			{Name: "Lose yb1", Status: domain.CheckPass},
			{Name: "Lose AZ-A", Status: domain.CheckPass},
			{Name: "Lose AZ-A + AZ-B", Status: domain.CheckFail, Reason: "Quorum lost"},
			{Name: "Lose eu-west-1", Status: domain.CheckFail, Reason: "Quorum lost"},
		},
		RPO:            "0 for tolerated failures",
		Recommendation: "Add cross-region replica placement if regional failure tolerance is required.",
	}
	require.NoError(t, Resilience(&buf, res, Options{NoColor: true}))
	assertGolden(t, "resilience", buf.Bytes())

	buf.Reset()
	exp := &domain.Explanation{
		Code:           domain.CodeTabletImbalance,
		What:           "Tablet distribution across TServers is uneven.",
		WhyItMatters:   "Uneven tablet placement can create CPU, memory, disk and network hotspots.",
		CurrentCluster: []string{"yb-01    642", "yb-02    641", "yb-03    914  ← 42% above average"},
		PossibleCauses: []string{"recently added/removed node", "rebalance still running"},
		NextSteps:      []string{"Check cluster balancing status", "Check placement configuration"},
	}
	require.NoError(t, Explain(&buf, exp, Options{NoColor: true}))
	assertGolden(t, "explain", buf.Bytes())

	buf.Reset()
	poc := &domain.POCReport{
		Workload: domain.Workload{DatabaseBytes: 4_200_000_000_000, TPS: 38200, ReadPct: 72, WritePct: 28, Connections: 850},
		Nodes:    6, Regions: 1, AZs: 3, RF: 3,
		Checks: []domain.POCCheck{
			{Name: "Node failure", Status: domain.CheckPass},
			{Name: "AZ failure", Status: domain.CheckPass},
			{Name: "P99 < 20ms requirement", Status: domain.CheckWarn},
		},
		Passed:     7,
		Total:      8,
		ResultLine: "7/8 acceptance criteria passed.",
	}
	require.NoError(t, POC(&buf, poc, Options{NoColor: true}))
	assertGolden(t, "poc", buf.Bytes())
}

func TestCommasAndBytes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "1,284", commas(1284))
	require.Equal(t, "12", commas(12))
	require.Equal(t, "n/a", bytesHuman(0))
	require.Equal(t, "4.2 TB", bytesHuman(4_200_000_000_000))
	require.Equal(t, "n/a", ms(0))
	require.Equal(t, "38ms", ms(38))
	require.Equal(t, "12G", bytesShort(12<<30))
	require.Equal(t, "2.0G", bytesShort(1<<31))
	require.Equal(t, "n/a", bytesShort(0))
	require.Equal(t, "74K", bytesShort(75653))
	require.Equal(t, "17%", debtPct(1<<31, 12<<30))
}

func TestLoadBalancerLine(t *testing.T) {
	t.Parallel()
	require.Empty(t, loadBalancerLine(domain.LoadBalancer{}))
	require.Equal(t, "enabled, still running", loadBalancerLine(domain.LoadBalancer{
		Known: true, Enabled: true, HasIdle: true, Idle: false,
	}))
	require.Equal(t, "enabled, idle unknown", loadBalancerLine(domain.LoadBalancer{
		Known: true, Enabled: true,
	}))
	require.Equal(t, "disabled", loadBalancerLine(domain.LoadBalancer{Known: true, Enabled: false}))
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}
