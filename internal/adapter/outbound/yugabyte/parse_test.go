package yugabyte

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

const clusterConfigJSON = `{
  "version": 4,
  "replication_info": {
    "live_replicas": {
      "num_replicas": 3,
      "placement_blocks": [
        {"cloud_info": {"placement_cloud": "aws", "placement_region": "eu-west-1", "placement_zone": "eu-west-1a"}},
        {"cloud_info": {"placement_cloud": "aws", "placement_region": "eu-west-1", "placement_zone": "eu-west-1b"}},
        {"cloud_info": {"placement_cloud": "aws", "placement_region": "eu-west-1", "placement_zone": "eu-west-1c"}}
      ]
    }
  }
}`

const mastersJSON = `{
  "masters": [
    {
      "instance_id": {"permanent_uuid": "m1"},
      "registration": {
        "private_rpc_addresses": [{"host": "yb-1", "port": 7100}],
        "http_addresses": [{"host": "yb-1", "port": 7000}],
        "placement_cloud": "aws",
        "placement_region": "eu-west-1",
        "placement_zone": "eu-west-1a"
      },
      "role": "LEADER"
    },
    {
      "instance_id": {"permanent_uuid": "m2"},
      "registration": {
        "private_rpc_addresses": [{"host": "yb-2", "port": 7100}],
        "http_addresses": [{"host": "yb-2", "port": 7000}],
        "placement_cloud": "aws",
        "placement_region": "eu-west-1",
        "placement_zone": "eu-west-1b"
      },
      "role": "FOLLOWER"
    }
  ]
}`

const tserversJSON = `{
  "placement-1": {
    "yb-1:9000": {
      "status": "ALIVE",
      "uptime_seconds": 100,
      "ram_used_bytes": 1024,
      "read_ops_per_sec": 10,
      "write_ops_per_sec": 5,
      "uuid": "n1",
      "path_metrics": [{"used_space": 80, "total_space": 100}],
      "cloud_info": {"cloud": "aws", "region": "eu-west-1", "zone": "eu-west-1a"}
    },
    "yb-2:9000": {
      "status": "DEAD",
      "uuid": "n2",
      "cloud_info": {"cloud": "aws", "region": "eu-west-1", "zone": "eu-west-1b"}
    }
  }
}`

const entitiesJSON = `{
  "keyspaces": [{"keyspace_id": "ks1", "keyspace_name": "yugabyte"}],
  "tables": [{"table_id": "t1", "keyspace_id": "ks1", "table_name": "orders", "table_type": "PGSQL_TABLE_TYPE", "state": "RUNNING"}],
  "tablets": [
    {
      "table_id": "t1",
      "tablet_id": "tab1",
      "state": "RUNNING",
      "leader": "n1",
      "replicas": [
        {"server_uuid": "n1", "addr": "yb-1:9100"},
        {"server_uuid": "n2", "addr": "yb-2:9100"},
        {"server_uuid": "n3", "addr": "yb-3:9100"}
      ]
    }
  ]
}`

const healthJSON = `{"under_replicated_tablets": ["tab1"], "leaderless_tablets": []}`
const replJSON = `{"leaderless_tablets": [{"tablet_uuid": "tab9"}], "underreplicated_tablets": [{"tablet_uuid": "tab8"}]}`

func TestParseSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap, err := parseSnapshot(now, []byte(mastersJSON), []byte(tserversJSON), []byte(clusterConfigJSON), []byte(healthJSON), []byte(replJSON), []byte(entitiesJSON))
	require.NoError(t, err)
	require.Equal(t, 3, snap.ReplicationFactor)
	require.Len(t, snap.PlacementBlocks, 3)
	require.Len(t, snap.Masters, 2)
	require.Equal(t, domain.RoleLeader, snap.Masters[0].Role)
	require.Len(t, snap.TServers, 2)
	require.Equal(t, 80.0, snap.TServers[0].DiskUsedPct+snap.TServers[1].DiskUsedPct) // one of them is 80
	require.Len(t, snap.Tablets, 1)
	require.True(t, snap.Tablets[0].HasLeader())
	require.Equal(t, []domain.TabletID{"tab8"}, snap.UnderReplicatedIDs)
	require.Equal(t, []domain.TabletID{"tab9"}, snap.LeaderlessIDs)
}

func TestParsePrometheus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		p99  float64
	}{
		{
			name: "legacy 0.99 microseconds",
			text: "handler_latency_yb_ysqlserver_SQLProcessor_SelectStmt{quantile=\"0.99\"} 38000\n",
			p99:  38,
		},
		{
			name: "2025 p99 label",
			text: "handler_latency_yb_ysqlserver_SQLProcessor_SelectStmt{quantile=\"p99\"} 21000\n",
			p99:  21,
		},
		{
			name: "quantile 99 string",
			text: "handler_latency_yb_ysqlserver_SQLProcessor_SelectStmt{quantile=\"99\"} 18000\n",
			p99:  18,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := `
rocksdb_pending_compaction_bytes 2048
sst_files_size 4096
cpu_usage 12.5
not-a-metric
` + tc.text
			rt := parsePrometheus(text)
			require.Equal(t, int64(2048), rt.PendingCompactionBytes)
			require.Equal(t, int64(4096), rt.SSTFileBytes)
			require.Equal(t, tc.p99, rt.P99MS)
			require.Equal(t, 12.5, rt.CPUPercent)
		})
	}
}

func TestParsePrometheusSumsTabletSSTAndDropsTimestamp(t *testing.T) {
	t.Parallel()
	rt := parsePrometheus(`
rocksdb_current_version_sst_files_size{table_id="a"} 100 1786622054295
rocksdb_current_version_sst_files_size{table_id="b"} 50 1786622054295
rocksdb_total_sst_files_size{table_id="a"} 999 1786622054295
rocksdb_pending_compaction_bytes{tablet="a"} 10 1786622054295
rocksdb_pending_compaction_bytes{tablet="b"} 5 1786622054295
`)
	require.Equal(t, int64(150), rt.SSTFileBytes)
	require.Equal(t, int64(15), rt.PendingCompactionBytes)
}

func TestParseLoadBalancer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		idleJSON []byte
		varz     []byte
		prom     string
		known    bool
		enabled  bool
		hasIdle  bool
		wantIdle bool
	}{
		{
			name:     "varz enabled and not idle",
			idleJSON: []byte(`{"is_idle":false}`),
			varz:     []byte(`{"flags":[{"name":"enable_load_balancing","value":"true"}]}`),
			known:    true,
			enabled:  true,
			hasIdle:  true,
		},
		{
			name:     "plain true idle body",
			idleJSON: []byte(`true`),
			known:    true,
			hasIdle:  true,
			wantIdle: true,
		},
		{
			name:    "disabled in varz",
			varz:    []byte(`{"flags":[{"name":"enable_load_balancing","value":"false"}]}`),
			known:   true,
			enabled: false,
		},
		{
			name:    "prometheus flag when varz missing",
			prom:    "is_load_balancing_enabled{export_type=\"master\"} 1\n",
			known:   true,
			enabled: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lb := applyLoadBalancerProm(parseLoadBalancer(tc.idleJSON, tc.varz), tc.prom)
			require.Equal(t, tc.known, lb.Known)
			require.Equal(t, tc.enabled, lb.Enabled)
			require.Equal(t, tc.hasIdle, lb.HasIdle)
			require.Equal(t, tc.wantIdle, lb.Idle)
		})
	}
}

func TestPickAllowlistFlags(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"flags":[
		{"name":"ysql_enable_packed_row","value":"true"},
		{"name":"ignored_flag","value":"1"},
		{"name":"enable_automatic_tablet_splitting","value":"false"}
	]}`)
	got := pickAllowlistFlags(parseVarzFlags(raw), []string{"ysql_enable_packed_row", "enable_automatic_tablet_splitting"})
	require.Equal(t, map[string]string{
		"ysql_enable_packed_row":            "true",
		"enable_automatic_tablet_splitting": "false",
	}, got)
	require.Nil(t, pickAllowlistFlags(parseVarzFlags(raw), []string{"nope"}))
}

func TestParseHealthEmpty(t *testing.T) {
	t.Parallel()
	u, l := parseHealth(nil)
	require.Nil(t, u)
	require.Nil(t, l)
	u, l = parseHealth([]byte("not-json"))
	require.Nil(t, u)
	require.Nil(t, l)
}

func TestParseTServersLiveAPIShape(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
	  "cluster-uuid": {
	    "yugabyte-n1:9000": {
	      "status": "ALIVE",
	      "uptime_seconds": 42,
	      "ram_used_bytes": 44032000,
	      "read_ops_per_sec": 1.5,
	      "write_ops_per_sec": 0.2,
	      "permanent_uuid": "bb509be72a574a2399bc31d7dfa2304e",
	      "cloud": "aws",
	      "region": "eu-west-1",
	      "zone": "eu-west-1a",
	      "path_metrics": [{"path": "/data", "space_used": 80, "total_space_size": 100}]
	    }
	  }
	}`)
	ts, err := parseTServers(raw)
	require.NoError(t, err)
	require.Len(t, ts, 1)
	require.Equal(t, domain.NodeID("bb509be72a574a2399bc31d7dfa2304e"), ts[0].ID)
	require.Equal(t, "yugabyte-n1", ts[0].Name)
	require.Equal(t, domain.StatusAlive, ts[0].Status)
	require.Equal(t, domain.Placement{Cloud: "aws", Region: "eu-west-1", Zone: "eu-west-1a"}, ts[0].Placement)
	require.InDelta(t, 80.0, ts[0].DiskUsedPct, 0.01)
}

func TestAssignTServerUUIDsAndEmptyStatus(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"": {"yb-1:9000": {"uptime_seconds": 1, "cloud_info": {"cloud":"aws","region":"eu-west-1","zone":"a"}}}}`)
	ts, err := parseTServers(raw)
	require.NoError(t, err)
	require.Len(t, ts, 1)
	require.Equal(t, domain.StatusAlive, ts[0].Status)
	assignTServerUUIDs(ts, map[string]domain.NodeID{"yb-1": "uuid-1"})
	require.Equal(t, domain.NodeID("uuid-1"), ts[0].ID)
}

func TestParseEntitiesRejectsHTML(t *testing.T) {
	t.Parallel()
	_, _, _, err := parseEntities([]byte("<html>nope</html>"))
	require.Error(t, err)
}

func TestJoinURL(t *testing.T) {
	t.Parallel()
	require.Equal(t, "http://yb-1:7000/api/v1/masters", joinURL("yb-1:7000", "/api/v1/masters"))
	require.Equal(t, "https://yb-1:7000/x", joinURL("https://yb-1:7000/", "/x"))
}

func TestAnyLoopback(t *testing.T) {
	t.Parallel()
	require.True(t, anyLoopback([]string{"127.0.0.1:7000"}))
	require.True(t, anyLoopback([]string{"http://localhost:7000"}))
	require.False(t, anyLoopback([]string{"yugabyte-n1:7000"}))
}
