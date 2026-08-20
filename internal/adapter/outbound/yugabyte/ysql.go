package yugabyte

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

type ysqlServerRow struct {
	Host   string
	UUID   string
	Cloud  string
	Region string
	Zone   string
}

type ysqlTabletRow struct {
	TabletID string
	DBName   string
	RelName  string
	Leader   string
	Replicas []string
}

func ysqlDSN(addr, user, password, database, sslmode string) string {
	if user == "" {
		user = "yugabyte"
	}
	if database == "" {
		database = "yugabyte"
	}
	if sslmode == "" {
		sslmode = "disable"
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   addr,
		Path:   "/" + database,
	}
	if password != "" {
		u.User = url.UserPassword(user, password)
	} else {
		u.User = url.User(user)
	}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	u.RawQuery = q.Encode()
	return u.String()
}

func overlayYSQL(snap *domain.Snapshot, servers []ysqlServerRow, tablets []ysqlTabletRow) {
	if snap == nil {
		return
	}
	hostToID := map[string]domain.NodeID{}
	for _, ts := range snap.TServers {
		hostToID[ts.Host] = ts.ID
		hostToID[ts.Name] = ts.ID
	}
	for _, s := range servers {
		if s.UUID != "" {
			hostToID[s.Host] = domain.NodeID(s.UUID)
		}
		for i := range snap.TServers {
			if snap.TServers[i].Host != s.Host && snap.TServers[i].Name != s.Host {
				continue
			}
			if s.UUID != "" {
				snap.TServers[i].ID = domain.NodeID(s.UUID)
			}
			if snap.TServers[i].Placement.Region == "" && s.Region != "" {
				snap.TServers[i].Placement = domain.Placement{Cloud: s.Cloud, Region: s.Region, Zone: s.Zone}
			}
		}
	}
	if len(tablets) == 0 {
		return
	}
	out := make([]domain.Tablet, 0, len(tablets))
	for _, t := range tablets {
		if len(t.Replicas) == 0 {
			continue
		}
		leaderHost := hostOfAddr(t.Leader)
		leaderID := hostToID[leaderHost]
		peers := make([]domain.TabletPeer, 0, len(t.Replicas))
		for _, r := range t.Replicas {
			h := hostOfAddr(r)
			id := hostToID[h]
			if id == "" {
				id = domain.NodeID(h)
			}
			role := domain.RoleFollower
			if h == leaderHost || r == t.Leader {
				role = domain.RoleLeader
				leaderID = id
			}
			peers = append(peers, domain.TabletPeer{TServerID: id, Role: role})
		}
		out = append(out, domain.Tablet{
			ID:        domain.TabletID(t.TabletID),
			TableName: t.RelName,
			LeaderID:  leaderID,
			Peers:     peers,
			State:     "RUNNING",
		})
	}
	if len(out) > 0 {
		snap.Tablets = out
	}
}

func hostOfAddr(addr string) string {
	host, _, ok := strings.Cut(addr, ":")
	if ok {
		return host
	}
	return addr
}

func (c *Client) enrichYSQL(ctx context.Context, snap *domain.Snapshot) {
	if c.ysqlAddr == "" {
		return
	}

	// Separate connections: cancelling a catalog query leaves pgx unable to run the next one.
	if err := c.withYSQL(ctx, func(qctx context.Context, conn ysqlConn) error {
		p99, slow, err := queryPgStatP99(qctx, conn, c.p99WarnMS)
		if err != nil {
			return err
		}
		if snap.Performance.SlowQueries == 0 {
			snap.Performance.SlowQueries = slow
		}
		if p99 > 0 && snap.Performance.P99YSQLMS == 0 {
			snap.Performance.P99YSQLMS = p99
			snap.Performance.P99Source = "pg_stat_statements"
		}
		return nil
	}); err != nil {
		c.logger.Warn("pg_stat_statements unavailable", "err", err)
	}

	var tablets []ysqlTabletRow
	if err := c.withYSQL(ctx, func(qctx context.Context, conn ysqlConn) error {
		var qerr error
		tablets, qerr = queryTabletMetadata(qctx, conn)
		return qerr
	}); err != nil {
		c.logger.Warn("yb_tablet_metadata unavailable", "err", err)
	}

	var servers []ysqlServerRow
	if needsYBServers(snap) {
		if err := c.withYSQL(ctx, func(qctx context.Context, conn ysqlConn) error {
			var qerr error
			servers, qerr = queryYBServers(qctx, conn)
			return qerr
		}); err != nil {
			c.logger.Warn("yb_servers unavailable", "err", err)
		}
	}
	if len(servers) > 0 || len(tablets) > 0 {
		overlayYSQL(snap, servers, tablets)
	}
}

func (c *Client) withYSQL(ctx context.Context, fn func(context.Context, ysqlConn) error) error {
	timeout := c.ysqlTimeout
	connectCtx, cancelConnect := withTimeout(ctx, timeout)
	conn, err := pgx.Connect(connectCtx, ysqlDSN(c.ysqlAddr, c.ysqlUser, c.ysqlPassword, c.ysqlDB, c.ysqlSSLMode))
	cancelConnect()
	if err != nil {
		return fmt.Errorf("ysql connect: %w", err)
	}
	defer func() {
		closeCtx, closeCancel := withTimeout(context.WithoutCancel(ctx), timeout)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()
	qctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	return fn(qctx, conn)
}

func needsYBServers(snap *domain.Snapshot) bool {
	if snap == nil || len(snap.TServers) == 0 {
		return true
	}
	for _, ts := range snap.TServers {
		if ts.Placement.Region == "" {
			return true
		}
	}
	return false
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

type ysqlConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func queryYBServers(ctx context.Context, conn ysqlConn) ([]ysqlServerRow, error) {
	rows, err := conn.Query(ctx, `SELECT host, uuid::text, cloud, region, zone FROM yb_servers()`)
	if err != nil {
		return nil, fmt.Errorf("query yb_servers: %w", err)
	}
	defer rows.Close()
	out := make([]ysqlServerRow, 0)
	for rows.Next() {
		var r ysqlServerRow
		if err := rows.Scan(&r.Host, &r.UUID, &r.Cloud, &r.Region, &r.Zone); err != nil {
			return nil, fmt.Errorf("scan yb_servers: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate yb_servers: %w", err)
	}
	return out, nil
}

func queryTabletMetadata(ctx context.Context, conn ysqlConn) ([]ysqlTabletRow, error) {
	rows, err := conn.Query(ctx, `
		SELECT tablet_id, coalesce(db_name, ''), coalesce(relname, ''), coalesce(leader, ''), coalesce(replicas, '{}')
		FROM yb_tablet_metadata
		WHERE replicas IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("query yb_tablet_metadata: %w", err)
	}
	defer rows.Close()
	out := make([]ysqlTabletRow, 0)
	for rows.Next() {
		var r ysqlTabletRow
		if err := rows.Scan(&r.TabletID, &r.DBName, &r.RelName, &r.Leader, &r.Replicas); err != nil {
			return nil, fmt.Errorf("scan yb_tablet_metadata: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate yb_tablet_metadata: %w", err)
	}
	return out, nil
}

func queryPgStatP99(ctx context.Context, conn ysqlConn, slowMS float64) (p99 float64, slow int, err error) {
	err = conn.QueryRow(ctx, `
		SELECT coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY mean_exec_time), 0),
		       count(*) FILTER (WHERE mean_exec_time >= $1)
		FROM pg_stat_statements
		WHERE query NOT ILIKE '%pg_stat_statements%'`, slowMS).Scan(&p99, &slow)
	if err != nil {
		return 0, 0, fmt.Errorf("query pg_stat_statements: %w", err)
	}
	return p99, slow, nil
}
