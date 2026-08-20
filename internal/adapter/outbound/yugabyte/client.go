// Package yugabyte collects a cluster snapshot from YB-Master HTTP APIs.
package yugabyte

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// ErrNoLeader is returned when no configured Master reports itself as leader.
var ErrNoLeader = errors.New("yugabyte: no master leader found")

// Client talks to YB-Master (and optionally TServer) HTTP endpoints.
type Client struct {
	http            *http.Client
	masters         []string
	logger          *slog.Logger
	maxConc         int64
	tserverHTTPBase int
	ysqlAddr        string
	ysqlUser        string
	ysqlPassword    string
	ysqlDB          string
	ysqlSSLMode     string
	ysqlTimeout     time.Duration
	flagAllow       []string
	p99WarnMS       float64
	now             func() time.Time
}

// New builds a Client from config.
func New(cfg config.Config, logger *slog.Logger) (*Client, error) {
	if len(cfg.Masters) == 0 {
		return nil, errors.New("yugabyte: masters is empty")
	}
	if logger == nil {
		logger = slog.Default()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.TLSCAFile != "" || cfg.TLSSkipVerify {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSSkipVerify {
			tlsCfg.InsecureSkipVerify = true
		}
		if cfg.TLSCAFile != "" {
			pem, err := os.ReadFile(cfg.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("read tls ca: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("yugabyte: invalid tls ca pem")
			}
			tlsCfg.RootCAs = pool
		}
		transport.TLSClientConfig = tlsCfg
	}
	base := cfg.LoopbackTServerPortBase
	if base < 1 {
		base = 9000
	}
	flagAllow := make([]string, 0, len(cfg.FlagAllowlist))
	for _, f := range cfg.FlagAllowlist {
		if f.Name != "" {
			flagAllow = append(flagAllow, f.Name)
		}
	}
	return &Client{
		http: &http.Client{
			Timeout:   cfg.HTTPTimeout,
			Transport: transport,
		},
		masters:         cfg.Masters,
		logger:          logger,
		maxConc:         int64(cfg.MaxConcurrency),
		tserverHTTPBase: base,
		ysqlAddr:        cfg.YSQLAddr,
		ysqlUser:        cfg.YSQLUser,
		ysqlPassword:    cfg.YSQLPassword,
		ysqlDB:          cfg.YSQLDatabase,
		ysqlSSLMode:     cfg.YSQLSSLMode,
		ysqlTimeout:     cfg.YSQLTimeout,
		flagAllow:       flagAllow,
		p99WarnMS:       cfg.Thresholds.P99WarnMS,
		now:             time.Now,
	}, nil
}

// Collect gathers topology, tablets, replication, and optional TServer metrics.
func (c *Client) Collect(ctx context.Context) (*domain.Snapshot, error) {
	leader, err := c.findLeader(ctx)
	if err != nil {
		return nil, fmt.Errorf("find master leader: %w", err)
	}
	c.logger.Info("using master leader", "addr", leader)

	var (
		mastersJSON  []byte
		tserversJSON []byte
		configJSON   []byte
		healthJSON   []byte
		replJSON     []byte
		entitiesJSON []byte
		idleJSON     []byte
		varzJSON     []byte
		masterProm   []byte
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/masters")
		mastersJSON = b
		return wrap("masters", err)
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/tablet-servers")
		tserversJSON = b
		return wrap("tablet-servers", err)
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/cluster-config")
		configJSON = b
		return wrap("cluster-config", err)
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/health-check")
		if err != nil {
			c.logger.Debug("health-check unavailable", "err", err)
			return nil
		}
		healthJSON = b
		return nil
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/tablet-replication")
		if err != nil {
			c.logger.Debug("tablet-replication unavailable", "err", err)
			return nil
		}
		replJSON = b
		return nil
	})
	g.Go(func() error {
		b, err := c.getEntities(gctx, leader)
		entitiesJSON = b
		return wrap("dump-entities", err)
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/is-load-balancer-idle")
		if err != nil {
			c.logger.Debug("is-load-balancer-idle unavailable", "err", err)
			return nil
		}
		idleJSON = b
		return nil
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/api/v1/varz")
		if err != nil {
			c.logger.Debug("varz unavailable", "err", err)
			return nil
		}
		varzJSON = b
		return nil
	})
	g.Go(func() error {
		b, err := c.get(gctx, leader, "/prometheus-metrics")
		if err != nil {
			c.logger.Debug("master prometheus-metrics unavailable", "err", err)
			return nil
		}
		masterProm = b
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("collect master apis: %w", err)
	}

	snap, err := parseSnapshot(c.now(), mastersJSON, tserversJSON, configJSON, healthJSON, replJSON, entitiesJSON)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}

	if err := c.enrichMetrics(ctx, snap); err != nil {
		c.logger.Debug("tserver metrics skipped", "err", err)
	}
	snap.LoadBalancer = applyLoadBalancerProm(parseLoadBalancer(idleJSON, varzJSON), string(masterProm))
	c.enrichYSQL(ctx, snap)
	return snap, nil
}

func (c *Client) findLeader(ctx context.Context) (string, error) {
	var mu sync.Mutex
	var found string
	g, gctx := errgroup.WithContext(ctx)
	for _, m := range c.masters {
		addr := m
		g.Go(func() error {
			b, err := c.get(gctx, addr, "/api/v1/is-leader")
			if err != nil {
				c.logger.Debug("master not reachable", "addr", addr, "err", err)
				return nil
			}
			var body struct {
				IsLeader bool `json:"is_leader"`
			}
			if err := json.Unmarshal(b, &body); err != nil {
				// some versions return plain true/false
				if strings.Contains(strings.ToLower(string(b)), "true") {
					body.IsLeader = true
				}
			}
			if body.IsLeader {
				mu.Lock()
				found = addr
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", fmt.Errorf("find leader: %w", err)
	}
	if found != "" {
		return found, nil
	}
	// fall back to the first master that answers /api/v1/masters
	for _, addr := range c.masters {
		if _, err := c.get(ctx, addr, "/api/v1/masters"); err == nil {
			return addr, nil
		}
	}
	return "", ErrNoLeader
}

func (c *Client) enrichMetrics(ctx context.Context, snap *domain.Snapshot) error {
	if snap.Performance.Nodes == nil {
		snap.Performance.Nodes = map[string]domain.NodeRuntime{}
	}
	loopback := anyLoopback(c.masters)
	order := make([]domain.TServer, 0, len(snap.TServers))
	order = append(order, snap.TServers...)
	sort.Slice(order, func(i, j int) bool { return order[i].Name < order[j].Name })

	sem := semaphore.NewWeighted(c.maxConc)
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for i := range order {
		ts := order[i]
		idx := i
		if ts.HTTPAddr == "" || !ts.Alive() {
			continue
		}
		if err := sem.Acquire(gctx, 1); err != nil {
			return fmt.Errorf("metrics semaphore: %w", err)
		}
		g.Go(func() error {
			defer sem.Release(1)
			addrs := []string{ts.HTTPAddr}
			if loopback {
				addrs = append(addrs, fmt.Sprintf("127.0.0.1:%d", c.tserverHTTPBase+idx))
			}
			var body []byte
			var varz []byte
			for _, addr := range addrs {
				b, berr := c.get(gctx, addr, "/prometheus-metrics")
				v, verr := c.get(gctx, addr, "/api/v1/varz")
				if berr != nil && verr != nil {
					continue
				}
				if berr == nil {
					body = b
				}
				if verr == nil {
					varz = v
				}
				break
			}
			if body == nil && varz == nil {
				return nil
			}
			rt := parsePrometheus(string(body))
			if ts.DiskUsedPct > 0 && rt.DiskPercent == 0 {
				rt.DiskPercent = ts.DiskUsedPct
			}
			if flags := pickAllowlistFlags(parseVarzFlags(varz), c.flagAllow); len(flags) > 0 {
				rt.Flags = flags
			}
			mu.Lock()
			prev := snap.Performance.Nodes[display(ts)]
			if prev.Flags != nil && rt.Flags == nil {
				rt.Flags = prev.Flags
			}
			snap.Performance.Nodes[display(ts)] = rt.NodeRuntime
			if rt.P99MS > snap.Performance.P99YSQLMS {
				snap.Performance.P99YSQLMS = rt.P99MS
				snap.Performance.P99Source = "prometheus"
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("collect tserver metrics: %w", err)
	}
	return nil
}

func (c *Client) getEntities(ctx context.Context, leader string) ([]byte, error) {
	for _, path := range []string{"/dump-entities", "/api/v1/dump-entities"} {
		b, err := c.get(ctx, leader, path)
		if err != nil {
			c.logger.Debug("dump-entities path failed", "path", path, "err", err)
			continue
		}
		if len(b) > 0 && b[0] == '{' {
			return b, nil
		}
	}
	return nil, fmt.Errorf("yugabyte: dump-entities did not return JSON")
}

func (c *Client) get(ctx context.Context, addr, path string) ([]byte, error) {
	url := joinURL(addr, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body %s: %w", url, err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http get %s: status %d", url, resp.StatusCode)
	}
	return body, nil
}

func joinURL(addr, path string) string {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + path
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

func display(ts domain.TServer) string {
	if ts.Name != "" {
		return ts.Name
	}
	return ts.Host
}

func anyLoopback(addrs []string) bool {
	for _, addr := range addrs {
		host := strings.TrimPrefix(addr, "http://")
		host = strings.TrimPrefix(host, "https://")
		host, _, _ = strings.Cut(host, "/")
		host, _, _ = strings.Cut(host, ":")
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return true
		}
	}
	return false
}
