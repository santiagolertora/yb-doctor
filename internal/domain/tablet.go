package domain

import "time"

// Tablet is a shard of a table. Each replica set is an independent Raft group.
type Tablet struct {
	ID        TabletID     `json:"id"`
	TableID   TableID      `json:"table_id"`
	TableName string       `json:"table_name"`
	State     string       `json:"state"`
	LeaderID  NodeID       `json:"leader_id"`
	Peers     []TabletPeer `json:"peers"`
	SizeBytes int64        `json:"size_bytes"`
	ReadOps   float64      `json:"read_ops"`
	WriteOps  float64      `json:"write_ops"`
}

// TabletPeer is one Raft member of a tablet.
type TabletPeer struct {
	TServerID NodeID        `json:"tserver_id"`
	Role      ReplicaRole   `json:"role"`
	State     string        `json:"state"`
	Lag       time.Duration `json:"lag"`
}

// VotingPeers returns peers that still count toward Raft quorum.
func (t Tablet) VotingPeers() []TabletPeer {
	out := make([]TabletPeer, 0, len(t.Peers))
	for _, p := range t.Peers {
		if p.State == "TOMBSTONED" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// HasLeader reports whether a LEADER peer is present.
func (t Tablet) HasLeader() bool {
	if t.LeaderID != "" {
		return true
	}
	for _, p := range t.VotingPeers() {
		if p.Role == RoleLeader {
			return true
		}
	}
	return false
}

// ReplicaCount is the number of voting peers.
func (t Tablet) ReplicaCount() int {
	return len(t.VotingPeers())
}

// Ops is combined read+write operations per second for hotspot detection.
func (t Tablet) Ops() float64 {
	return t.ReadOps + t.WriteOps
}
