package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func TestDiffReportsNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, DiffReports("x", nil, &domain.HealthReport{}))
	require.Nil(t, DiffReports("x", &domain.HealthReport{}, nil))
}

func TestDiffReportsScoreAndLeaders(t *testing.T) {
	t.Parallel()
	prev := &domain.HealthReport{
		Score:    100,
		Topology: domain.TopologySummary{MastersHealthy: 3, MastersTotal: 3, TServersHealthy: 3, TServersTotal: 3},
		Tablets: domain.TabletSummary{PerNode: []domain.NodeTabletStats{
			{Name: "n1", Leaders: 10},
			{Name: "n3", Leaders: 10},
		}},
		Findings: []domain.Finding{{Code: domain.CodeSingleRegion}},
	}
	curr := &domain.HealthReport{
		Score:    92,
		Topology: domain.TopologySummary{MastersHealthy: 2, MastersTotal: 3, TServersHealthy: 3, TServersTotal: 3},
		Tablets: domain.TabletSummary{PerNode: []domain.NodeTabletStats{
			{Name: "n1", Leaders: 15},
			{Name: "n3", Leaders: 0},
		}},
		Findings: []domain.Finding{
			{Code: domain.CodeSingleRegion},
			{Code: domain.CodeLeaderImbalance},
		},
		Raft: domain.RaftSummary{UnderReplicated: 4},
	}
	d := DiffReports("before.json", prev, curr)
	require.Equal(t, 100, d.ScoreFrom)
	require.Equal(t, 92, d.ScoreTo)
	require.Equal(t, "3/3", d.MastersFrom)
	require.Equal(t, "2/3", d.MastersTo)
	require.Equal(t, []domain.FindingCode{domain.CodeLeaderImbalance}, d.FindingsAdded)
	require.Empty(t, d.FindingsRemoved)
	require.Equal(t, []domain.NodeCountDelta{
		{Name: "n1", From: 10, To: 15},
		{Name: "n3", From: 10, To: 0},
	}, d.Leaders)
}

type seqCollector struct {
	snaps []*domain.Snapshot
	i     int
}

func (c *seqCollector) Collect(context.Context) (*domain.Snapshot, error) {
	idx := c.i
	if idx >= len(c.snaps) {
		idx = len(c.snaps) - 1
	}
	c.i++
	cp := *c.snaps[idx]
	return &cp, nil
}

func TestWatchAnalyzeDiffsFirstAndLast(t *testing.T) {
	t.Parallel()
	a := threeAZHealthy()
	b := threeAZHealthy()
	b.Masters[0].Healthy = false
	svc, err := New(testCfg(t), &seqCollector{snaps: []*domain.Snapshot{a, b}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	rep, err := svc.WatchAnalyze(ctx, 10*time.Millisecond, nil)
	require.NoError(t, err)
	require.NotNil(t, rep)
	require.NotNil(t, rep.Diff)
	require.Equal(t, "watch start", rep.Diff.Baseline)
	require.Equal(t, "3/3", rep.Diff.MastersFrom)
	require.Equal(t, "2/3", rep.Diff.MastersTo)
}
