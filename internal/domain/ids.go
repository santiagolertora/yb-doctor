// Package domain holds YugabyteDB cluster types and invariants.
package domain

import "fmt"

// NodeID identifies a YB-Master or YB-TServer process.
type NodeID string

// TabletID identifies a tablet (the unit of sharding and Raft).
type TabletID string

// TableID identifies a DocDB table.
type TableID string

// FindingCode is a stable identifier used by `yb-doctor explain`.
type FindingCode string

// ReplicaRole is the Raft role of a tablet peer.
type ReplicaRole string

// NodeStatus is liveness as observed by the Master leader.
type NodeStatus string

// Severity ranks a diagnostic finding.
type Severity string

// CheckStatus is the result of a resilience or POC check.
type CheckStatus string

const (
	// RoleLeader is the Raft leader of a tablet group.
	RoleLeader ReplicaRole = "LEADER"
	// RoleFollower is a Raft follower of a tablet group.
	RoleFollower ReplicaRole = "FOLLOWER"
	// RoleUnknown is an unrecognized replica role.
	RoleUnknown ReplicaRole = "UNKNOWN"

	// StatusAlive means the Master is receiving heartbeats from the TServer.
	StatusAlive NodeStatus = "ALIVE"
	// StatusDead means the TServer is not heartbeating.
	StatusDead NodeStatus = "DEAD"

	// SeverityHigh is an availability or data-risk finding.
	SeverityHigh Severity = "HIGH"
	// SeverityMedium is an operational hotspot or imbalance.
	SeverityMedium Severity = "MEDIUM"
	// SeverityLow is informational (for example single-region).
	SeverityLow Severity = "LOW"

	// CheckPass is a passing resilience or POC check.
	CheckPass CheckStatus = "PASS"
	// CheckFail is a failing check (quorum lost, criterion missed).
	CheckFail CheckStatus = "FAIL"
	// CheckWarn is a degraded but non-fatal check.
	CheckWarn CheckStatus = "WARN"

	// CodeLeaderImbalance is emitted when Raft leaders are skewed across TServers.
	CodeLeaderImbalance FindingCode = "leader-imbalance"
	// CodeTabletImbalance is emitted when tablet peers are skewed across TServers.
	CodeTabletImbalance FindingCode = "tablet-imbalance"
	// CodeUnderReplicated is emitted when tablets have fewer replicas than RF.
	CodeUnderReplicated FindingCode = "under-replicated"
	// CodeLeaderless is emitted when tablets have no Raft leader.
	CodeLeaderless FindingCode = "leaderless-tablets"
	// CodeSlowFollowers is emitted when followers lag the leader WAL.
	CodeSlowFollowers FindingCode = "slow-followers"
	// CodeCompactionPressure is emitted when DocDB compaction is backlogged.
	CodeCompactionPressure FindingCode = "compaction-pressure"
	// CodeSSTImbalance is emitted when SST file bytes are badly skewed across TServers.
	CodeSSTImbalance FindingCode = "sst-imbalance"
	// CodeHotTablets is emitted when a few tablets absorb most operations.
	CodeHotTablets FindingCode = "hot-tablets"
	// CodeDeadTServer is emitted when a TServer is not heartbeating.
	CodeDeadTServer FindingCode = "dead-tserver"
	// CodeMasterQuorum is emitted when YB-Master lacks a healthy majority.
	CodeMasterQuorum FindingCode = "master-quorum"
	// CodeDiskPressure is emitted when a TServer data disk is filling up.
	CodeDiskPressure FindingCode = "disk-pressure"
	// CodeSingleRegion is emitted when every TServer is in one region.
	CodeSingleRegion FindingCode = "single-region"
	// CodeP99Latency is emitted when YSQL P99 exceeds the configured target.
	CodeP99Latency FindingCode = "p99-latency"
)

// Placement is a cloud failure-domain coordinate.
type Placement struct {
	Cloud  string `json:"cloud"`
	Region string `json:"region"`
	Zone   string `json:"zone"`
}

func (p Placement) String() string {
	if p.Cloud == "" && p.Region == "" && p.Zone == "" {
		return "unspecified"
	}
	return fmt.Sprintf("%s.%s.%s", p.Cloud, p.Region, p.Zone)
}

// Key returns cloud.region.zone for grouping.
func (p Placement) Key() string {
	return p.String()
}

// RegionKey returns cloud.region.
func (p Placement) RegionKey() string {
	if p.Cloud == "" && p.Region == "" {
		return "unspecified"
	}
	return fmt.Sprintf("%s.%s", p.Cloud, p.Region)
}
