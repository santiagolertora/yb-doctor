// Package app implements YugabyteDB diagnostic use cases.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// SnapshotCollector loads a point-in-time cluster view.
type SnapshotCollector interface {
	Collect(ctx context.Context) (*domain.Snapshot, error)
}

// Service is the application entry point for analyze, resilience, explain, and poc.
type Service struct {
	cfg       config.Config
	collector SnapshotCollector
	logger    *slog.Logger
}

// New constructs a Service. collector must be non-nil.
func New(cfg config.Config, collector SnapshotCollector, logger *slog.Logger) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if collector == nil {
		return nil, fmt.Errorf("app: collector is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, collector: collector, logger: logger}, nil
}

// Analyze produces the cluster health report.
func (s *Service) Analyze(ctx context.Context) (*domain.HealthReport, error) {
	snap, err := s.collector.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect snapshot: %w", err)
	}
	return s.analyzeSnapshot(snap), nil
}

func (s *Service) analyzeSnapshot(snap *domain.Snapshot) *domain.HealthReport {
	topo := summarizeTopology(snap)
	tablets := analyzeTablets(snap, s.cfg.Thresholds)
	raft := analyzeRaft(snap, s.cfg.Thresholds)
	perf := analyzePerformance(snap, s.cfg.Thresholds)
	perf.HotTablets = tablets.HotTablets
	findings := collectFindings(snap, tablets, raft, s.cfg)
	sortFindings(findings)
	score := scoreReport(findings, raft, s.cfg.Scoring)
	return &domain.HealthReport{
		Snapshot:    *snap,
		Topology:    topo,
		Tablets:     tablets,
		Raft:        raft,
		Performance: perf,
		Findings:    findings,
		Score:       score,
	}
}

func summarizeTopology(snap *domain.Snapshot) domain.TopologySummary {
	mh, mt := snap.HealthyMasters()
	th, tt := snap.HealthyTServers()
	return domain.TopologySummary{
		Nodes:             len(snap.TServers),
		MastersHealthy:    mh,
		MastersTotal:      mt,
		TServersHealthy:   th,
		TServersTotal:     tt,
		ReplicationFactor: snap.ReplicationFactor,
		Regions:           snap.Regions(),
		Zones:             snap.Zones(),
	}
}

func sortFindings(findings []domain.Finding) {
	rank := map[domain.Severity]int{
		domain.SeverityHigh:   0,
		domain.SeverityMedium: 1,
		domain.SeverityLow:    2,
	}
	sort.SliceStable(findings, func(i, j int) bool {
		ri, rj := rank[findings[i].Severity], rank[findings[j].Severity]
		if ri != rj {
			return ri < rj
		}
		return findings[i].Code < findings[j].Code
	})
}
