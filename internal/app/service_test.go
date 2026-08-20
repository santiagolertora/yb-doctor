package app

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func testCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Scenario = "inline"
	require.NoError(t, cfg.Validate())
	return cfg
}

func TestNewRejectsNilCollector(t *testing.T) {
	t.Parallel()
	_, err := New(testCfg(t), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	_, err := New(config.Defaults(), staticCollector{snap: threeAZHealthy()}, nil)
	require.Error(t, err)
}

func TestAnalyzeLeaderImbalanceAndCompaction(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: sixNodeImbalance()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	report, err := svc.Analyze(t.Context())
	require.NoError(t, err)
	require.Equal(t, 6, report.Topology.Nodes)
	require.Equal(t, 3, report.Topology.MastersHealthy)
	require.Equal(t, 1284, report.Tablets.Total)
	require.Equal(t, domain.CheckWarn, report.Tablets.LeaderImbalance)
	require.Greater(t, report.Tablets.LeaderRatio, 2.0)
	require.Equal(t, "yb-01", report.Tablets.HottestNode)
	require.Equal(t, "yb-05", report.Tablets.ColdestNode)
	require.Equal(t, 7, report.Raft.SlowFollowers)
	require.Equal(t, 3, report.Tablets.HotTablets)
	require.Contains(t, report.Performance.CompactionPressure, "yb-03")
	require.Less(t, report.Score, 100)
	require.Greater(t, report.Score, 70)

	codes := map[domain.FindingCode]domain.Finding{}
	for _, f := range report.Findings {
		codes[f.Code] = f
	}
	require.Contains(t, codes, domain.CodeLeaderImbalance)
	require.Contains(t, codes, domain.CodeCompactionPressure)
	require.NotContains(t, codes, domain.CodeDiskPressure)
	require.NotContains(t, codes, domain.CodeSSTImbalance)
	require.Equal(t, domain.SeverityHigh, codes[domain.CodeCompactionPressure].Severity)
	require.Contains(t, codes[domain.CodeCompactionPressure].Evidence[0], "pending compaction")
}

func TestAnalyzeHealthyCluster(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: threeAZHealthy()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	report, err := svc.Analyze(t.Context())
	require.NoError(t, err)
	require.Equal(t, domain.CheckPass, report.Tablets.LeaderImbalance)
	require.Equal(t, domain.CheckPass, report.Tablets.TabletImbalance)
	require.Equal(t, 0, report.Raft.Leaderless)
	require.GreaterOrEqual(t, report.Score, 90)
}

func TestAnalyzeIgnoresUnplacedTablets(t *testing.T) {
	t.Parallel()
	snap := threeAZHealthy()
	snap.Tablets = append(snap.Tablets, domain.Tablet{
		ID:    "sys-catalog",
		State: "RUNNING",
	})
	svc, err := New(testCfg(t), staticCollector{snap: snap}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	report, err := svc.Analyze(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, report.Raft.Leaderless)
	require.Equal(t, 0, report.Raft.UnderReplicated)
	require.Equal(t, 0, report.Tablets.Leaderless)
	require.Equal(t, 0, report.Tablets.UnderReplicated)
}

func TestResilienceIgnoresUnplacedTablets(t *testing.T) {
	t.Parallel()
	snap := threeAZHealthy()
	snap.Tablets = append(snap.Tablets, domain.Tablet{ID: "sys-catalog", State: "RUNNING"})
	svc, err := New(testCfg(t), staticCollector{snap: snap}, slog.Default())
	require.NoError(t, err)
	rep, err := svc.Resilience(t.Context())
	require.NoError(t, err)
	for _, sim := range rep.Simulations {
		if sim.Kind == "node" || sim.Kind == "az" {
			require.Equal(t, domain.CheckPass, sim.Status, sim.Name)
		}
	}
}

func TestAnalyzeCollectError(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{err: errors.New("boom")}, slog.Default())
	require.NoError(t, err)
	_, err = svc.Analyze(t.Context())
	require.ErrorContains(t, err, "collect snapshot")
}

func TestResilienceAZAndRegion(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: threeAZHealthy()}, slog.Default())
	require.NoError(t, err)
	rep, err := svc.Resilience(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, rep.TopologyTree)
	byKind := map[string][]domain.FailureSim{}
	for _, sim := range rep.Simulations {
		byKind[sim.Kind] = append(byKind[sim.Kind], sim)
	}
	for _, sim := range byKind["node"] {
		require.Equal(t, domain.CheckPass, sim.Status, sim.Name)
	}
	for _, sim := range byKind["az"] {
		require.Equal(t, domain.CheckPass, sim.Status, sim.Name)
	}
	require.NotEmpty(t, byKind["az-pair"])
	require.Equal(t, domain.CheckFail, byKind["az-pair"][0].Status)
	require.NotEmpty(t, byKind["region"])
	require.Equal(t, domain.CheckFail, byKind["region"][0].Status)
	require.Contains(t, rep.Recommendation, "cross-region")
}

func TestResilienceDeadNodeCluster(t *testing.T) {
	t.Parallel()
	snap := threeAZHealthy()
	snap.TServers[0].Status = domain.StatusDead
	// remaining 2 replicas: quorum of 2 still holds for node loss of an already-dead node,
	// but losing another live node drops to 1.
	svc, err := New(testCfg(t), staticCollector{snap: snap}, slog.Default())
	require.NoError(t, err)
	rep, err := svc.Resilience(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, rep.Simulations)
}

func TestExplainUnknownAndKnown(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: sixNodeImbalance()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = svc.Explain(t.Context(), "not-a-thing")
	require.Error(t, err)
	exp, err := svc.Explain(t.Context(), "leader-imbalance")
	require.NoError(t, err)
	require.Equal(t, domain.CodeLeaderImbalance, exp.Code)
	require.NotEmpty(t, exp.What)
	require.NotEmpty(t, exp.CurrentCluster)
	require.Contains(t, KnownFindingCodes(), "leader-imbalance")
}

func TestPOCHealthyPassesMost(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: threeAZHealthy()}, slog.Default())
	require.NoError(t, err)
	crit := domain.DefaultPOCCriteria()
	crit.MinTPS = 50000
	crit.MaxP99YSQLMS = 20
	rep, err := svc.POC(t.Context(), crit)
	require.NoError(t, err)
	require.GreaterOrEqual(t, rep.Passed, 6)
	require.Equal(t, 3, rep.AZs)
}

func TestPOCImbalanceWarns(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: sixNodeImbalance()}, slog.Default())
	require.NoError(t, err)
	rep, err := svc.POC(t.Context(), domain.DefaultPOCCriteria())
	require.NoError(t, err)
	found := false
	for _, c := range rep.Checks {
		if c.Name == "Leader distribution" {
			found = true
			require.Equal(t, domain.CheckFail, c.Status)
		}
	}
	require.True(t, found)
}

func TestMaxMinRatio(t *testing.T) {
	t.Parallel()
	r, _, _ := maxMinRatio(nil)
	require.Equal(t, 1.0, r)
	r, _, _ = maxMinRatio([]int{0, 0})
	require.Equal(t, 1.0, r)
	r, maxI, minI := maxMinRatio([]int{10, 5, 2})
	require.Equal(t, 5.0, r)
	require.Equal(t, 0, maxI)
	require.Equal(t, 2, minI)
}

func TestLeaderImbalanceLoadBalancerEvidence(t *testing.T) {
	t.Parallel()
	sum := domain.TabletSummary{HottestNode: "yb-01", ColdestNode: "yb-05", LeaderRatio: 2.6}
	tests := []struct {
		name string
		lb   domain.LoadBalancer
		want string
	}{
		{name: "still running", lb: domain.LoadBalancer{Known: true, Enabled: true, HasIdle: true, Idle: false}, want: "Master load balancer is still running"},
		{name: "idle leftover", lb: domain.LoadBalancer{Known: true, Enabled: true, HasIdle: true, Idle: true}, want: "Master load balancer is idle; this skew is leftover placement"},
		{name: "disabled", lb: domain.LoadBalancer{Known: true, Enabled: false}, want: "Master load balancing is disabled; this skew will not self-heal"},
		{name: "enabled idle unknown", lb: domain.LoadBalancer{Known: true, Enabled: true}, want: "Master load balancing is enabled; idle state is not exposed on this Master"},
		{name: "unknown", lb: domain.LoadBalancer{}, want: "consider checking leader balancing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := leaderImbalanceEvidence(sum, tc.lb)
			require.Contains(t, got, tc.want)
		})
	}
}

func TestScoreCaps(t *testing.T) {
	t.Parallel()
	sc := config.Defaults().Scoring
	raft := domain.RaftSummary{Leaderless: 100, UnderReplicated: 100, SlowFollowers: 100}
	score := scoreReport(nil, raft, sc)
	want := sc.Start - sc.LeaderlessCap - sc.UnderReplicatedCap
	if sc.SlowFollower > 0 {
		slow := raft.SlowFollowers * sc.SlowFollower
		if sc.SlowFollowerCap > 0 && slow > sc.SlowFollowerCap {
			slow = sc.SlowFollowerCap
		}
		want -= slow
	}
	require.Equal(t, want, score)
}

func TestLeaderlessAndDeadFindings(t *testing.T) {
	t.Parallel()
	snap := threeAZHealthy()
	snap.TServers[2].Status = domain.StatusDead
	snap.Tablets[0].LeaderID = ""
	snap.Tablets[0].Peers = []domain.TabletPeer{
		{TServerID: "n1", Role: domain.RoleFollower},
		{TServerID: "n2", Role: domain.RoleFollower},
	}
	snap.Masters[1].Healthy = false
	snap.Masters[2].Healthy = false
	svc, err := New(testCfg(t), staticCollector{snap: snap}, slog.Default())
	require.NoError(t, err)
	report, err := svc.Analyze(t.Context())
	require.NoError(t, err)
	codes := map[domain.FindingCode]struct{}{}
	for _, f := range report.Findings {
		codes[f.Code] = struct{}{}
	}
	require.Contains(t, codes, domain.CodeDeadTServer)
	require.Contains(t, codes, domain.CodeLeaderless)
	require.Contains(t, codes, domain.CodeMasterQuorum)
	require.Contains(t, codes, domain.CodeUnderReplicated)
}

func TestCompactionBacklogNeedsRatioOrEmptyNode(t *testing.T) {
	t.Parallel()
	th := config.Defaults().Thresholds
	require.True(t, compactionBacklog(domain.NodeRuntime{PendingCompactionBytes: 2 << 30}, th))
	require.True(t, compactionBacklog(domain.NodeRuntime{PendingCompactionBytes: 2 << 30, SSTFileBytes: 12 << 30}, th))
	require.False(t, compactionBacklog(domain.NodeRuntime{PendingCompactionBytes: 2 << 30, SSTFileBytes: 800 << 30}, th))
	require.False(t, compactionBacklog(domain.NodeRuntime{PendingCompactionBytes: 100 << 20, SSTFileBytes: 1 << 30}, th))
}

func TestSSTImbalanceFinding(t *testing.T) {
	t.Parallel()
	snap := threeAZHealthy()
	snap.Performance.Nodes = map[string]domain.NodeRuntime{
		"yb-1": {SSTFileBytes: 40 << 30},
		"yb-2": {SSTFileBytes: 10 << 30},
		"yb-3": {SSTFileBytes: 10 << 30},
	}
	svc, err := New(testCfg(t), staticCollector{snap: snap}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	report, err := svc.Analyze(t.Context())
	require.NoError(t, err)
	require.Equal(t, domain.CheckWarn, report.Tablets.SSTImbalance)
	codes := map[domain.FindingCode]domain.Finding{}
	for _, f := range report.Findings {
		codes[f.Code] = f
	}
	require.Contains(t, codes, domain.CodeSSTImbalance)
	require.Contains(t, codes[domain.CodeSSTImbalance].Evidence[0], "yb-1")
}

func TestFlagEvidenceSkipsDefaults(t *testing.T) {
	t.Parallel()
	allow := config.Defaults().FlagAllowlist
	snap := &domain.Snapshot{Performance: domain.Performance{Nodes: map[string]domain.NodeRuntime{
		"yb-1": {Flags: map[string]string{
			"ysql_enable_packed_row":                         "true",
			"enable_automatic_tablet_splitting":              "false",
			"rocksdb_compact_flush_rate_limit_bytes_per_sec": "1",
		}},
	}}}
	heat := flagEvidence(snap, "yb-1", allow, config.FlagTopicSplitting, config.FlagTopicStorage)
	require.Equal(t, []string{"enable_automatic_tablet_splitting=false (default true)"}, heat)
	compact := flagEvidence(snap, "yb-1", allow, config.FlagTopicCompaction)
	require.Equal(t, []string{"rocksdb_compact_flush_rate_limit_bytes_per_sec=1 (default 1073741824)"}, compact)
}
