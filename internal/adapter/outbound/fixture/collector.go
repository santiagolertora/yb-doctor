// Package fixture expands on-disk diagnostic scenarios into a cluster snapshot.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// Collector loads a scenario directory or JSON file.
type Collector struct {
	path string
	now  func() time.Time
}

// New returns a fixture collector rooted at path.
func New(path string) (*Collector, error) {
	if path == "" {
		return nil, fmt.Errorf("fixture: path is empty")
	}
	return &Collector{path: path, now: time.Now}, nil
}

// Collect reads and expands the scenario.
func (c *Collector) Collect(ctx context.Context) (*domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fixture collect: %w", err)
	}
	raw, err := os.ReadFile(c.scenarioFile())
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	snap, err := Expand(plan)
	if err != nil {
		return nil, err
	}
	snap.CollectedAt = c.now()
	return snap, nil
}

func (c *Collector) scenarioFile() string {
	info, err := os.Stat(c.path)
	if err == nil && info.IsDir() {
		return filepath.Join(c.path, "scenario.json")
	}
	return c.path
}

// Plan is the compact, human-editable scenario format.
type Plan struct {
	ReplicationFactor int                `json:"replication_factor"`
	Masters           []domain.Master    `json:"masters"`
	TServers          []domain.TServer   `json:"tservers"`
	Leaders           map[string]int     `json:"leaders"`
	Followers         map[string]int     `json:"followers"`
	SlowFollowers     int                `json:"slow_followers"`
	HotTablets        int                `json:"hot_tablets"`
	Leaderless        int                `json:"leaderless"`
	UnderReplicated   int                `json:"under_replicated"`
	Performance       domain.Performance `json:"performance"`
	Workload          domain.Workload    `json:"workload"`
	PlacementBlocks   []domain.Placement `json:"placement_blocks"`
}

// Expand turns a plan into a full snapshot with synthetic tablets.
func Expand(plan Plan) (*domain.Snapshot, error) {
	if len(plan.TServers) == 0 {
		return nil, fmt.Errorf("fixture: plan has no tservers")
	}
	rf := plan.ReplicationFactor
	if rf == 0 {
		rf = 3
	}
	byName := map[string]domain.NodeID{}
	for _, ts := range plan.TServers {
		byName[ts.Name] = ts.ID
		if ts.Name == "" {
			byName[ts.Host] = ts.ID
		}
	}
	leaders := remapCounts(plan.Leaders, byName)
	followers := remapCounts(plan.Followers, byName)
	if len(leaders) == 0 {
		leaders, followers = balancedCounts(plan.TServers, 9, rf)
	}
	tablets := expandTablets(leaders, followers, rf)
	applyFaults(tablets, plan)
	return &domain.Snapshot{
		ReplicationFactor: rf,
		PlacementBlocks:   plan.PlacementBlocks,
		Masters:           plan.Masters,
		TServers:          plan.TServers,
		Tablets:           tablets,
		Performance:       plan.Performance,
		Workload:          plan.Workload,
	}, nil
}

func remapCounts(in map[string]int, byName map[string]domain.NodeID) map[domain.NodeID]int {
	out := map[domain.NodeID]int{}
	for k, v := range in {
		if id, ok := byName[k]; ok {
			out[id] = v
			continue
		}
		out[domain.NodeID(k)] = v
	}
	return out
}

func balancedCounts(nodes []domain.TServer, tablets, rf int) (map[domain.NodeID]int, map[domain.NodeID]int) {
	leaders := map[domain.NodeID]int{}
	followers := map[domain.NodeID]int{}
	for i := 0; i < tablets; i++ {
		leader := nodes[i%len(nodes)].ID
		leaders[leader]++
		for r := 1; r < rf; r++ {
			followers[nodes[(i+r)%len(nodes)].ID]++
		}
	}
	return leaders, followers
}

func expandTablets(leaders, followers map[domain.NodeID]int, rf int) []domain.Tablet {
	ids := make([]domain.NodeID, 0, len(leaders))
	for id := range leaders {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	leaderQ := make([]domain.NodeID, 0)
	for _, id := range ids {
		for i := 0; i < leaders[id]; i++ {
			leaderQ = append(leaderQ, id)
		}
	}
	remainF := map[domain.NodeID]int{}
	for id, n := range followers {
		remainF[id] = n
	}
	tablets := make([]domain.Tablet, 0, len(leaderQ))
	for i, leader := range leaderQ {
		picked := []domain.NodeID{leader}
		for len(picked) < rf {
			var best domain.NodeID
			bestN := -1
			for id, n := range remainF {
				if contains(picked, id) || n <= 0 {
					continue
				}
				if n > bestN || (n == bestN && id < best) {
					bestN = n
					best = id
				}
			}
			if best == "" {
				for _, id := range ids {
					if !contains(picked, id) {
						best = id
						break
					}
				}
			}
			if best == "" {
				break
			}
			picked = append(picked, best)
			remainF[best]--
		}
		peers := make([]domain.TabletPeer, 0, len(picked))
		for j, id := range picked {
			role := domain.RoleFollower
			if j == 0 {
				role = domain.RoleLeader
			}
			peers = append(peers, domain.TabletPeer{TServerID: id, Role: role})
		}
		tablets = append(tablets, domain.Tablet{
			ID:       domain.TabletID(fmt.Sprintf("t%04d", i)),
			LeaderID: leader,
			Peers:    peers,
			ReadOps:  10,
		})
	}
	return tablets
}

func applyFaults(tablets []domain.Tablet, plan Plan) {
	slow := plan.SlowFollowers
	for i := range tablets {
		if slow <= 0 {
			break
		}
		for j := range tablets[i].Peers {
			if tablets[i].Peers[j].Role == domain.RoleFollower {
				tablets[i].Peers[j].Lag = 1500 * time.Millisecond
				slow--
				break
			}
		}
	}
	for i := 0; i < plan.HotTablets && i < len(tablets); i++ {
		tablets[i].WriteOps = 5000
	}
	for i := 0; i < plan.Leaderless && i < len(tablets); i++ {
		tablets[i].LeaderID = ""
		for j := range tablets[i].Peers {
			tablets[i].Peers[j].Role = domain.RoleFollower
		}
	}
	for i := 0; i < plan.UnderReplicated && i < len(tablets); i++ {
		if len(tablets[i].Peers) > 1 {
			tablets[i].Peers = tablets[i].Peers[:len(tablets[i].Peers)-1]
		}
	}
}

func contains(ids []domain.NodeID, id domain.NodeID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
