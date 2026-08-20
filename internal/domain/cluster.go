package domain

import "time"

// Snapshot is a point-in-time view of a YugabyteDB universe.
// Analyzers never talk to the cluster; they only read a Snapshot.
type Snapshot struct {
	CollectedAt        time.Time    `json:"collected_at"`
	ReplicationFactor  int          `json:"replication_factor"`
	PlacementBlocks    []Placement  `json:"placement_blocks"`
	Masters            []Master     `json:"masters"`
	TServers           []TServer    `json:"tservers"`
	Tables             []Table      `json:"tables"`
	Tablets            []Tablet     `json:"tablets"`
	UnderReplicatedIDs []TabletID   `json:"under_replicated_ids"`
	LeaderlessIDs      []TabletID   `json:"leaderless_ids"`
	Performance        Performance  `json:"performance"`
	Workload           Workload     `json:"workload"`
	LoadBalancer       LoadBalancer `json:"load_balancer"`
}

// Master is a YB-Master process (control plane).
type Master struct {
	ID        NodeID      `json:"id"`
	Host      string      `json:"host"`
	HTTPAddr  string      `json:"http_addr"`
	RPCAddr   string      `json:"rpc_addr"`
	Role      ReplicaRole `json:"role"`
	Placement Placement   `json:"placement"`
	Healthy   bool        `json:"healthy"`
}

// TServer is a YB-TServer process (data plane).
type TServer struct {
	ID           NodeID        `json:"id"`
	Name         string        `json:"name"`
	Host         string        `json:"host"`
	HTTPAddr     string        `json:"http_addr"`
	RPCAddr      string        `json:"rpc_addr"`
	Status       NodeStatus    `json:"status"`
	Placement    Placement     `json:"placement"`
	Uptime       time.Duration `json:"uptime"`
	RAMUsedBytes int64         `json:"ram_used_bytes"`
	DiskUsedPct  float64       `json:"disk_used_pct"`
	ReadOps      float64       `json:"read_ops"`
	WriteOps     float64       `json:"write_ops"`
}

// LoadBalancer is Master tablet-load-balancer state, when the Master exposes it.
type LoadBalancer struct {
	Known   bool `json:"known"`
	Enabled bool `json:"enabled"`
	HasIdle bool `json:"has_idle"`
	Idle    bool `json:"idle"`
}

// Alive reports whether the Master currently considers this TServer live.
func (t TServer) Alive() bool {
	return t.Status == StatusAlive
}

// Table is a DocDB table (YSQL or YCQL).
type Table struct {
	ID        TableID `json:"id"`
	Keyspace  string  `json:"keyspace"`
	Name      string  `json:"name"`
	TableType string  `json:"table_type"`
	State     string  `json:"state"`
}

// Performance is cluster-wide and per-node runtime telemetry.
type Performance struct {
	P50YSQLMS      float64                `json:"p50_ysql_ms"`
	P95YSQLMS      float64                `json:"p95_ysql_ms"`
	P99YSQLMS      float64                `json:"p99_ysql_ms"`
	P99Source      string                 `json:"p99_source,omitempty"`
	SlowQueries    int                    `json:"slow_queries"`
	ReadLatencyMS  float64                `json:"read_latency_ms"`
	WriteLatencyMS float64                `json:"write_latency_ms"`
	Nodes          map[string]NodeRuntime `json:"nodes"`
}

// NodeRuntime is RocksDB/DocDB and host pressure for one TServer.
type NodeRuntime struct {
	CPUPercent             float64           `json:"cpu_percent"`
	MemoryPercent          float64           `json:"memory_percent"`
	DiskPercent            float64           `json:"disk_percent"`
	PendingCompactionBytes int64             `json:"pending_compaction_bytes"`
	SSTFileBytes           int64             `json:"sst_file_bytes"`
	WriteLatencyDeltaPct   float64           `json:"write_latency_delta_pct"`
	Flags                  map[string]string `json:"flags,omitempty"`
}

// Workload is optional POC-oriented observed load.
type Workload struct {
	DatabaseBytes int64   `json:"database_bytes"`
	TPS           float64 `json:"tps"`
	ReadPct       float64 `json:"read_pct"`
	WritePct      float64 `json:"write_pct"`
	Connections   int     `json:"connections"`
}

// TServerByID returns the TServer with the given id, if present.
func (s Snapshot) TServerByID(id NodeID) (TServer, bool) {
	for _, ts := range s.TServers {
		if ts.ID == id {
			return ts, true
		}
	}
	return TServer{}, false
}

// TServerByName returns the TServer with the given display name or host.
func (s Snapshot) TServerByName(name string) (TServer, bool) {
	for _, ts := range s.TServers {
		if ts.Name == name || ts.Host == name {
			return ts, true
		}
	}
	return TServer{}, false
}

// HealthyMasters returns how many masters are healthy and the total count.
func (s Snapshot) HealthyMasters() (healthy, total int) {
	total = len(s.Masters)
	for _, m := range s.Masters {
		if m.Healthy {
			healthy++
		}
	}
	return healthy, total
}

// HealthyTServers returns how many tablet servers are alive and the total count.
func (s Snapshot) HealthyTServers() (healthy, total int) {
	total = len(s.TServers)
	for _, ts := range s.TServers {
		if ts.Alive() {
			healthy++
		}
	}
	return healthy, total
}

// Regions returns unique region keys from TServers.
func (s Snapshot) Regions() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, ts := range s.TServers {
		k := ts.Placement.RegionKey()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// Zones returns unique zone keys from TServers.
func (s Snapshot) Zones() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, ts := range s.TServers {
		k := ts.Placement.Key()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// QuorumSize is the Raft majority for the configured replication factor.
func (s Snapshot) QuorumSize() int {
	if s.ReplicationFactor < 1 {
		return 1
	}
	return s.ReplicationFactor/2 + 1
}
