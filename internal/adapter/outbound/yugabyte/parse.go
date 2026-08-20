package yugabyte

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

type parsedRuntime struct {
	domain.NodeRuntime
	P99MS float64
}

func parseSnapshot(
	now time.Time,
	mastersJSON, tserversJSON, configJSON, healthJSON, replJSON, entitiesJSON []byte,
) (*domain.Snapshot, error) {
	rf, blocks, err := parseClusterConfig(configJSON)
	if err != nil {
		return nil, fmt.Errorf("parse cluster-config: %w", err)
	}
	masters, err := parseMasters(mastersJSON)
	if err != nil {
		return nil, fmt.Errorf("parse masters: %w", err)
	}
	tservers, err := parseTServers(tserversJSON)
	if err != nil {
		return nil, fmt.Errorf("parse tablet-servers: %w", err)
	}
	tables, tablets, hostToUUID, err := parseEntities(entitiesJSON)
	if err != nil {
		return nil, fmt.Errorf("parse dump-entities: %w", err)
	}
	under, leaderless := parseHealth(healthJSON)
	u2, l2 := parseReplication(replJSON)
	if len(u2) > 0 {
		under = u2
	}
	if len(l2) > 0 {
		leaderless = l2
	}
	if rf == 0 {
		rf = 3
	}
	assignTServerUUIDs(tservers, hostToUUID)
	return &domain.Snapshot{
		CollectedAt:        now,
		ReplicationFactor:  rf,
		PlacementBlocks:    blocks,
		Masters:            masters,
		TServers:           tservers,
		Tables:             tables,
		Tablets:            tablets,
		UnderReplicatedIDs: under,
		LeaderlessIDs:      leaderless,
		Performance:        domain.Performance{Nodes: map[string]domain.NodeRuntime{}},
	}, nil
}

func parseClusterConfig(raw []byte) (int, []domain.Placement, error) {
	var body struct {
		ReplicationInfo struct {
			LiveReplicas struct {
				NumReplicas     int `json:"num_replicas"`
				PlacementBlocks []struct {
					CloudInfo struct {
						Cloud  string `json:"placement_cloud"`
						Region string `json:"placement_region"`
						Zone   string `json:"placement_zone"`
					} `json:"cloud_info"`
				} `json:"placement_blocks"`
			} `json:"live_replicas"`
		} `json:"replication_info"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, nil, fmt.Errorf("unmarshal cluster-config: %w", err)
	}
	blocks := make([]domain.Placement, 0, len(body.ReplicationInfo.LiveReplicas.PlacementBlocks))
	for _, b := range body.ReplicationInfo.LiveReplicas.PlacementBlocks {
		blocks = append(blocks, domain.Placement{
			Cloud:  b.CloudInfo.Cloud,
			Region: b.CloudInfo.Region,
			Zone:   b.CloudInfo.Zone,
		})
	}
	return body.ReplicationInfo.LiveReplicas.NumReplicas, blocks, nil
}

func parseMasters(raw []byte) ([]domain.Master, error) {
	var body struct {
		Masters []struct {
			InstanceID struct {
				PermanentUUID string `json:"permanent_uuid"`
			} `json:"instance_id"`
			Registration struct {
				PrivateRPC []struct {
					Host string `json:"host"`
					Port int    `json:"port"`
				} `json:"private_rpc_addresses"`
				HTTP []struct {
					Host string `json:"host"`
					Port int    `json:"port"`
				} `json:"http_addresses"`
				Cloud  string `json:"placement_cloud"`
				Region string `json:"placement_region"`
				Zone   string `json:"placement_zone"`
			} `json:"registration"`
			Role  string `json:"role"`
			Error any    `json:"error"`
		} `json:"masters"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("unmarshal masters: %w", err)
	}
	out := make([]domain.Master, 0, len(body.Masters))
	for _, m := range body.Masters {
		host := ""
		httpAddr := ""
		rpcAddr := ""
		if len(m.Registration.HTTP) > 0 {
			host = m.Registration.HTTP[0].Host
			httpAddr = fmt.Sprintf("%s:%d", m.Registration.HTTP[0].Host, m.Registration.HTTP[0].Port)
		}
		if len(m.Registration.PrivateRPC) > 0 {
			if host == "" {
				host = m.Registration.PrivateRPC[0].Host
			}
			rpcAddr = fmt.Sprintf("%s:%d", m.Registration.PrivateRPC[0].Host, m.Registration.PrivateRPC[0].Port)
		}
		role := domain.RoleFollower
		if strings.EqualFold(m.Role, "LEADER") {
			role = domain.RoleLeader
		}
		out = append(out, domain.Master{
			ID:       domain.NodeID(m.InstanceID.PermanentUUID),
			Host:     host,
			HTTPAddr: httpAddr,
			RPCAddr:  rpcAddr,
			Role:     role,
			Placement: domain.Placement{
				Cloud:  m.Registration.Cloud,
				Region: m.Registration.Region,
				Zone:   m.Registration.Zone,
			},
			Healthy: m.Error == nil || fmt.Sprint(m.Error) == "" || fmt.Sprint(m.Error) == "<nil>",
		})
	}
	return out, nil
}

func parseTServers(raw []byte) ([]domain.TServer, error) {
	var nested map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return nil, fmt.Errorf("unmarshal tablet-servers: %w", err)
	}
	out := make([]domain.TServer, 0)
	for _, group := range nested {
		for addr, item := range group {
			ts, err := parseOneTServer(addr, item)
			if err != nil {
				return nil, err
			}
			out = append(out, ts)
		}
	}
	return out, nil
}

func parseOneTServer(addr string, raw json.RawMessage) (domain.TServer, error) {
	var body struct {
		Status         string  `json:"status"`
		UptimeSeconds  int64   `json:"uptime_seconds"`
		RAMUsedBytes   int64   `json:"ram_used_bytes"`
		ReadOpsPerSec  float64 `json:"read_ops_per_sec"`
		WriteOpsPerSec float64 `json:"write_ops_per_sec"`
		PathMetrics    []struct {
			UsedSpace      int64 `json:"used_space"`
			TotalSpace     int64 `json:"total_space"`
			SpaceUsed      int64 `json:"space_used"`
			TotalSpaceSize int64 `json:"total_space_size"`
		} `json:"path_metrics"`
		CloudInfo struct {
			Cloud  string `json:"cloud"`
			Region string `json:"region"`
			Zone   string `json:"zone"`
		} `json:"cloud_info"`
		Cloud         string `json:"cloud"`
		Region        string `json:"region"`
		Zone          string `json:"zone"`
		UUID          string `json:"uuid"`
		PermanentUUID string `json:"permanent_uuid"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return domain.TServer{}, fmt.Errorf("unmarshal tserver %s: %w", addr, err)
	}
	host := addr
	if h, _, ok := strings.Cut(addr, ":"); ok {
		host = h
	}
	status := domain.StatusAlive
	if body.Status != "" && !strings.EqualFold(body.Status, "ALIVE") {
		status = domain.StatusDead
	}
	var used, total int64
	for _, p := range body.PathMetrics {
		used += firstPositive(p.UsedSpace, p.SpaceUsed)
		total += firstPositive(p.TotalSpace, p.TotalSpaceSize)
	}
	disk := 0.0
	if total > 0 {
		disk = float64(used) / float64(total) * 100
	}
	id := body.UUID
	if id == "" {
		id = body.PermanentUUID
	}
	if id == "" {
		id = addr
	}
	httpAddr := addr
	if !strings.Contains(httpAddr, ":") {
		httpAddr = host + ":9000"
	}
	cloud := body.CloudInfo.Cloud
	if cloud == "" {
		cloud = body.Cloud
	}
	region := body.CloudInfo.Region
	if region == "" {
		region = body.Region
	}
	zone := body.CloudInfo.Zone
	if zone == "" {
		zone = body.Zone
	}
	return domain.TServer{
		ID:           domain.NodeID(id),
		Name:         host,
		Host:         host,
		HTTPAddr:     httpAddr,
		Status:       status,
		Placement:    domain.Placement{Cloud: cloud, Region: region, Zone: zone},
		Uptime:       time.Duration(body.UptimeSeconds) * time.Second,
		RAMUsedBytes: body.RAMUsedBytes,
		DiskUsedPct:  disk,
		ReadOps:      body.ReadOpsPerSec,
		WriteOps:     body.WriteOpsPerSec,
	}, nil
}

func firstPositive(values ...int64) int64 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func parseEntities(raw []byte) ([]domain.Table, []domain.Tablet, map[string]domain.NodeID, error) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, nil, nil, fmt.Errorf("dump-entities: response is not JSON")
	}
	var body struct {
		Keyspaces []struct {
			ID   string `json:"keyspace_id"`
			Name string `json:"keyspace_name"`
		} `json:"keyspaces"`
		Tables []struct {
			ID         string `json:"table_id"`
			KeyspaceID string `json:"keyspace_id"`
			Name       string `json:"table_name"`
			TableType  string `json:"table_type"`
			State      string `json:"state"`
		} `json:"tables"`
		Tablets []struct {
			TableID  string `json:"table_id"`
			TabletID string `json:"tablet_id"`
			State    string `json:"state"`
			Leader   string `json:"leader"`
			Replicas []struct {
				ServerUUID string `json:"server_uuid"`
				Addr       string `json:"addr"`
				Role       string `json:"role"`
			} `json:"replicas"`
		} `json:"tablets"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal dump-entities: %w", err)
	}
	ks := map[string]string{}
	for _, k := range body.Keyspaces {
		ks[k.ID] = k.Name
	}
	tables := make([]domain.Table, 0, len(body.Tables))
	tableName := map[string]string{}
	for _, tb := range body.Tables {
		tables = append(tables, domain.Table{
			ID:        domain.TableID(tb.ID),
			Keyspace:  ks[tb.KeyspaceID],
			Name:      tb.Name,
			TableType: tb.TableType,
			State:     tb.State,
		})
		tableName[tb.ID] = tb.Name
	}
	hostToUUID := map[string]domain.NodeID{}
	tablets := make([]domain.Tablet, 0, len(body.Tablets))
	for _, t := range body.Tablets {
		peers := make([]domain.TabletPeer, 0, len(t.Replicas))
		leader := domain.NodeID(t.Leader)
		for _, r := range t.Replicas {
			role := domain.RoleFollower
			if strings.EqualFold(r.Role, "LEADER") || (t.Leader != "" && r.ServerUUID == t.Leader) {
				role = domain.RoleLeader
				leader = domain.NodeID(r.ServerUUID)
			}
			id := r.ServerUUID
			if id == "" {
				id = r.Addr
			}
			if r.ServerUUID != "" && r.Addr != "" {
				host, _, _ := strings.Cut(r.Addr, ":")
				hostToUUID[host] = domain.NodeID(r.ServerUUID)
			}
			peers = append(peers, domain.TabletPeer{
				TServerID: domain.NodeID(id),
				Role:      role,
			})
		}
		tablets = append(tablets, domain.Tablet{
			ID:        domain.TabletID(t.TabletID),
			TableID:   domain.TableID(t.TableID),
			TableName: tableName[t.TableID],
			State:     t.State,
			LeaderID:  leader,
			Peers:     peers,
		})
	}
	return tables, tablets, hostToUUID, nil
}

func assignTServerUUIDs(tservers []domain.TServer, hostToUUID map[string]domain.NodeID) {
	for i := range tservers {
		if id, ok := hostToUUID[tservers[i].Host]; ok {
			tservers[i].ID = id
		}
	}
}

func parseHealth(raw []byte) (under, leaderless []domain.TabletID) {
	if len(raw) == 0 {
		return nil, nil
	}
	var body struct {
		UnderReplicated []string `json:"under_replicated_tablets"`
		Leaderless      []string `json:"leaderless_tablets"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil
	}
	return toIDs(body.UnderReplicated), toIDs(body.Leaderless)
}

func parseReplication(raw []byte) (under, leaderless []domain.TabletID) {
	if len(raw) == 0 {
		return nil, nil
	}
	var body struct {
		Leaderless []struct {
			TabletUUID string `json:"tablet_uuid"`
		} `json:"leaderless_tablets"`
		Under []struct {
			TabletUUID string `json:"tablet_uuid"`
		} `json:"underreplicated_tablets"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil
	}
	for _, t := range body.Under {
		under = append(under, domain.TabletID(t.TabletUUID))
	}
	for _, t := range body.Leaderless {
		leaderless = append(leaderless, domain.TabletID(t.TabletUUID))
	}
	return under, leaderless
}

func toIDs(ss []string) []domain.TabletID {
	out := make([]domain.TabletID, 0, len(ss))
	for _, s := range ss {
		out = append(out, domain.TabletID(s))
	}
	return out
}

func parsePrometheus(text string) parsedRuntime {
	var rt parsedRuntime
	var sstSum, sstFallback int64
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := splitProm(line)
		if !ok {
			continue
		}
		switch {
		case strings.Contains(name, "pending_compaction") && !strings.Contains(name, "bucket"):
			rt.PendingCompactionBytes += int64(val)
		case strings.Contains(name, "rocksdb_current_version_sst_files_size"):
			sstSum += int64(val)
		case strings.Contains(name, "sst_files_size") && !strings.Contains(name, "total"):
			sstFallback = int64(val)
		case isYSQLP99Metric(name):
			ms := latencyToMS(name, val)
			if ms > rt.P99MS {
				rt.P99MS = ms
			}
		case strings.Contains(name, "cpu_usage"):
			rt.CPUPercent = val
		}
	}
	if sstSum > 0 {
		rt.SSTFileBytes = sstSum
	} else {
		rt.SSTFileBytes = sstFallback
	}
	return rt
}

func isYSQLP99Metric(name string) bool {
	if !isP99Quantile(name) {
		return false
	}
	n := strings.ToLower(name)
	return strings.Contains(n, "sqlprocessor") ||
		strings.Contains(n, "ysqlserver") ||
		strings.Contains(n, "ysql_") ||
		strings.Contains(n, "handler_latency_yb_ysql")
}

func isP99Quantile(name string) bool {
	return strings.Contains(name, `quantile="0.99"`) ||
		strings.Contains(name, `quantile="p99"`) ||
		strings.Contains(name, `quantile="99"`)
}

func latencyToMS(name string, val float64) float64 {
	if strings.Contains(name, "handler_latency") || strings.Contains(name, "_us") {
		return val / 1000
	}
	return val
}

func parseLoadBalancer(idleJSON, varzJSON []byte) domain.LoadBalancer {
	var lb domain.LoadBalancer
	if len(varzJSON) > 0 {
		if enabled, ok := varzFlagBool(varzJSON, "enable_load_balancing"); ok {
			lb.Known = true
			lb.Enabled = enabled
		}
	}
	if len(idleJSON) == 0 {
		return lb
	}
	var body struct {
		IsIdle *bool `json:"is_idle"`
		Idle   *bool `json:"idle"`
	}
	if err := json.Unmarshal(idleJSON, &body); err != nil {
		s := strings.TrimSpace(strings.ToLower(string(idleJSON)))
		if s == "true" || s == "false" {
			lb.HasIdle = true
			lb.Idle = s == "true"
			lb.Known = true
		}
		return lb
	}
	switch {
	case body.IsIdle != nil:
		lb.HasIdle = true
		lb.Idle = *body.IsIdle
		lb.Known = true
	case body.Idle != nil:
		lb.HasIdle = true
		lb.Idle = *body.Idle
		lb.Known = true
	}
	return lb
}

func applyLoadBalancerProm(lb domain.LoadBalancer, text string) domain.LoadBalancer {
	if text == "" {
		return lb
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := splitProm(line)
		if !ok {
			continue
		}
		if promMetricName(name) != "is_load_balancing_enabled" {
			continue
		}
		enabled := val != 0
		if !lb.Known {
			lb.Known = true
			lb.Enabled = enabled
			continue
		}
		if enabled {
			lb.Enabled = true
		}
	}
	return lb
}

func promMetricName(s string) string {
	if i := strings.Index(s, "{"); i >= 0 {
		return s[:i]
	}
	return s
}

func varzFlagBool(raw []byte, name string) (bool, bool) {
	flags := parseVarzFlags(raw)
	v, ok := flags[name]
	if !ok {
		return false, false
	}
	return strings.EqualFold(v, "true") || v == "1", true
}

func parseVarzFlags(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var body struct {
		Flags []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	out := make(map[string]string, len(body.Flags))
	for _, f := range body.Flags {
		if f.Name == "" {
			continue
		}
		out[f.Name] = f.Value
	}
	return out
}

func pickAllowlistFlags(all map[string]string, names []string) map[string]string {
	if len(all) == 0 || len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v, ok := all[n]; ok {
			out[n] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitProm(line string) (string, float64, bool) {
	i := strings.LastIndex(line, " ")
	if i < 0 {
		return "", 0, false
	}
	last, err := strconv.ParseFloat(line[i+1:], 64)
	if err != nil {
		return "", 0, false
	}
	rest := strings.TrimSpace(line[:i])
	j := strings.LastIndex(rest, " ")
	if j >= 0 {
		if mid, err := strconv.ParseFloat(rest[j+1:], 64); err == nil {
			return rest[:j], mid, true
		}
	}
	return rest, last, true
}
