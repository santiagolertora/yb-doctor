package domain

// Finding is a scored diagnostic with evidence.
type Finding struct {
	Severity Severity    `json:"severity"`
	Code     FindingCode `json:"code"`
	Title    string      `json:"title"`
	Summary  string      `json:"summary"`
	Evidence []string    `json:"evidence"`
	Node     string      `json:"node,omitempty"`
}

// HealthReport is the output of `yb-doctor analyze`.
type HealthReport struct {
	Snapshot    Snapshot           `json:"snapshot"`
	Topology    TopologySummary    `json:"topology"`
	Tablets     TabletSummary      `json:"tablets"`
	Raft        RaftSummary        `json:"raft"`
	Performance PerformanceSummary `json:"performance"`
	Findings    []Finding          `json:"findings"`
	Score       int                `json:"score"`
	Diff        *HealthDiff        `json:"diff,omitempty"`
}

// TopologySummary is control-plane vs data-plane headcount.
type TopologySummary struct {
	Nodes             int      `json:"nodes"`
	MastersHealthy    int      `json:"masters_healthy"`
	MastersTotal      int      `json:"masters_total"`
	TServersHealthy   int      `json:"tservers_healthy"`
	TServersTotal     int      `json:"tservers_total"`
	ReplicationFactor int      `json:"replication_factor"`
	Regions           []string `json:"regions"`
	Zones             []string `json:"zones"`
}

// NodeTabletStats is per-TServer tablet, leader, and DocDB size.
type NodeTabletStats struct {
	Name                   string `json:"name"`
	NodeID                 NodeID `json:"node_id"`
	Leaders                int    `json:"leaders"`
	Followers              int    `json:"followers"`
	Total                  int    `json:"total"`
	SSTBytes               int64  `json:"sst_bytes"`
	PendingCompactionBytes int64  `json:"pending_compaction_bytes"`
}

// TabletSummary is distribution and imbalance.
type TabletSummary struct {
	Total           int               `json:"total"`
	UnderReplicated int               `json:"under_replicated"`
	Leaderless      int               `json:"leaderless"`
	LeaderImbalance CheckStatus       `json:"leader_imbalance"`
	TabletImbalance CheckStatus       `json:"tablet_imbalance"`
	HotTablets      int               `json:"hot_tablets"`
	PerNode         []NodeTabletStats `json:"per_node"`
	LeaderRatio     float64           `json:"leader_ratio"`
	TabletRatio     float64           `json:"tablet_ratio"`
	HottestNode     string            `json:"hottest_node"`
	ColdestNode     string            `json:"coldest_node"`
	SSTImbalance    CheckStatus       `json:"sst_imbalance"`
	SSTRatio        float64           `json:"sst_ratio"`
	SSTHottestNode  string            `json:"sst_hottest_node"`
	SSTColdestNode  string            `json:"sst_coldest_node"`
}

// RaftSummary is replication health across tablet Raft groups.
type RaftSummary struct {
	Leaderless      int `json:"leaderless"`
	UnderReplicated int `json:"under_replicated"`
	SlowFollowers   int `json:"slow_followers"`
	FailedPeers     int `json:"failed_peers"`
}

// PerformanceSummary is the analyze-view of latency and storage pressure.
type PerformanceSummary struct {
	P99YSQLMS          float64  `json:"p99_ysql_ms"`
	P99Source          string   `json:"p99_source,omitempty"`
	SlowQueries        int      `json:"slow_queries"`
	HotTablets         int      `json:"hot_tablets"`
	CompactionPressure string   `json:"compaction_pressure"`
	CompactionNodes    []string `json:"compaction_nodes"`
}
