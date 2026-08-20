package yugabyte

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/config"
)

func TestNewRequiresMasters(t *testing.T) {
	t.Parallel()
	_, err := New(config.Defaults(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
}

func TestCollectFromFakeMaster(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/is-leader", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"is_leader": true}`))
	})
	mux.HandleFunc("/api/v1/masters", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mastersJSON))
	})
	mux.HandleFunc("/api/v1/tablet-servers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(tserversJSON))
	})
	mux.HandleFunc("/api/v1/cluster-config", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(clusterConfigJSON))
	})
	mux.HandleFunc("/api/v1/health-check", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(healthJSON))
	})
	mux.HandleFunc("/api/v1/tablet-replication", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/dump-entities", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(entitiesJSON))
	})
	mux.HandleFunc("/api/v1/varz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"flags":[{"name":"enable_load_balancing","value":"true"}]}`))
	})
	mux.HandleFunc("/prometheus-metrics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("is_load_balancing_enabled 1\nrocksdb_pending_compaction_bytes 10\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := config.Defaults()
	cfg.Masters = []string{strings.TrimPrefix(srv.URL, "http://")}
	cfg.Scenario = ""
	client, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	snap, err := client.Collect(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, snap.ReplicationFactor)
	require.NotEmpty(t, snap.Masters)
	require.NotEmpty(t, snap.Tablets)
	require.True(t, snap.LoadBalancer.Known)
	require.True(t, snap.LoadBalancer.Enabled)
}

func TestCollectNoLeader(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.Masters = []string{strings.TrimPrefix(srv.URL, "http://")}
	client, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = client.Collect(t.Context())
	require.ErrorIs(t, err, ErrNoLeader)
}

func TestFindLeaderPlainTrue(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/is-leader", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`true`))
	})
	mux.HandleFunc("/api/v1/masters", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mastersJSON))
	})
	mux.HandleFunc("/api/v1/tablet-servers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(tserversJSON))
	})
	mux.HandleFunc("/api/v1/cluster-config", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(clusterConfigJSON))
	})
	mux.HandleFunc("/api/v1/health-check", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/dump-entities", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(entitiesJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.Masters = []string{strings.TrimPrefix(srv.URL, "http://")}
	client, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	snap, err := client.Collect(t.Context())
	require.NoError(t, err)
	require.NotNil(t, snap)
}

func TestHTTPStatusError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/is-leader" {
			_, _ = w.Write([]byte(`{"is_leader": true}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.Masters = []string{strings.TrimPrefix(srv.URL, "http://")}
	client, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = client.Collect(t.Context())
	require.Error(t, err)
}
