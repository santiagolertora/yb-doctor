package domain

// HealthDiff is analyze compared to a previous report (file or earlier --watch sample).
type HealthDiff struct {
	Baseline            string           `json:"baseline"`
	ScoreFrom           int              `json:"score_from"`
	ScoreTo             int              `json:"score_to"`
	MastersFrom         string           `json:"masters_from"`
	MastersTo           string           `json:"masters_to"`
	TServersFrom        string           `json:"tservers_from"`
	TServersTo          string           `json:"tservers_to"`
	UnderReplicatedFrom int              `json:"under_replicated_from"`
	UnderReplicatedTo   int              `json:"under_replicated_to"`
	LeaderlessFrom      int              `json:"leaderless_from"`
	LeaderlessTo        int              `json:"leaderless_to"`
	P99From             float64          `json:"p99_from"`
	P99To               float64          `json:"p99_to"`
	FindingsAdded       []FindingCode    `json:"findings_added"`
	FindingsRemoved     []FindingCode    `json:"findings_removed"`
	Leaders             []NodeCountDelta `json:"leaders"`
}

// NodeCountDelta is a per-node leader (or similar) count change.
type NodeCountDelta struct {
	Name string `json:"name"`
	From int    `json:"from"`
	To   int    `json:"to"`
}
