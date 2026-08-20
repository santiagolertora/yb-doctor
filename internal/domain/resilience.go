package domain

// ResilienceReport is the output of `yb-doctor resilience`.
type ResilienceReport struct {
	Snapshot       Snapshot       `json:"snapshot"`
	TopologyTree   []RegionBranch `json:"topology_tree"`
	Simulations    []FailureSim   `json:"simulations"`
	RPO            string         `json:"rpo"`
	Recommendation string         `json:"recommendation"`
}

// RegionBranch is a region → AZ → node tree for display.
type RegionBranch struct {
	Name  string     `json:"name"`
	Zones []AZBranch `json:"zones"`
}

// AZBranch is an availability zone and the TServers in it.
type AZBranch struct {
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
}

// FailureSim is one hypothetical failure and whether Raft quorum survives.
type FailureSim struct {
	Name          string      `json:"name"`
	Kind          string      `json:"kind"`
	Lost          []string    `json:"lost"`
	Status        CheckStatus `json:"status"`
	TabletsFailed int         `json:"tablets_failed"`
	Reason        string      `json:"reason"`
}

// Explanation is the output of `yb-doctor explain <code>`.
type Explanation struct {
	Code           FindingCode `json:"code"`
	What           string      `json:"what"`
	WhyItMatters   string      `json:"why_it_matters"`
	CurrentCluster []string    `json:"current_cluster"`
	PossibleCauses []string    `json:"possible_causes"`
	NextSteps      []string    `json:"next_steps"`
}

// POCCriteria is the acceptance checklist for `yb-doctor poc`.
type POCCriteria struct {
	Name                string  `json:"name"`
	MinNodes            int     `json:"min_nodes"`
	MinAZs              int     `json:"min_azs"`
	ReplicationFactor   int     `json:"replication_factor"`
	TolerateNodeFailure bool    `json:"tolerate_node_failure"`
	TolerateAZFailure   bool    `json:"tolerate_az_failure"`
	MaxP99YSQLMS        float64 `json:"max_p99_ysql_ms"`
	MinTPS              float64 `json:"min_tps"`
	BalancedTablets     bool    `json:"balanced_tablets"`
	BalancedLeaders     bool    `json:"balanced_leaders"`
}

// DefaultPOCCriteria is the common three-AZ RF=3 POC bar.
func DefaultPOCCriteria() POCCriteria {
	return POCCriteria{
		Name:                "YugabyteDB POC",
		MinNodes:            3,
		MinAZs:              3,
		ReplicationFactor:   3,
		TolerateNodeFailure: true,
		TolerateAZFailure:   true,
		MaxP99YSQLMS:        20,
		MinTPS:              0,
		BalancedTablets:     true,
		BalancedLeaders:     true,
	}
}

// POCCheck is one row in the POC validation table.
type POCCheck struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

// POCReport is the output of `yb-doctor poc`.
type POCReport struct {
	Criteria   POCCriteria `json:"criteria"`
	Workload   Workload    `json:"workload"`
	Nodes      int         `json:"nodes"`
	Regions    int         `json:"regions"`
	AZs        int         `json:"azs"`
	RF         int         `json:"rf"`
	Checks     []POCCheck  `json:"checks"`
	Passed     int         `json:"passed"`
	Total      int         `json:"total"`
	ResultLine string      `json:"result_line"`
}
