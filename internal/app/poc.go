package app

import (
	"context"
	"fmt"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// POC builds a proof-of-concept readiness report against acceptance criteria.
func (s *Service) POC(ctx context.Context, criteria domain.POCCriteria) (*domain.POCReport, error) {
	snap, err := s.collector.Collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect snapshot: %w", err)
	}
	health := s.analyzeSnapshot(snap)
	res := simulateResilience(snap)
	return buildPOC(health, res, criteria), nil
}

func buildPOC(health *domain.HealthReport, res *domain.ResilienceReport, criteria domain.POCCriteria) *domain.POCReport {
	if criteria.Name == "" {
		criteria = domain.DefaultPOCCriteria()
	}
	checks := make([]domain.POCCheck, 0, 8)

	nodeOK := health.Topology.TServersTotal >= criteria.MinNodes
	checks = append(checks, statusCheck("Node count", nodeOK, fmt.Sprintf("%d nodes (min %d)", health.Topology.TServersTotal, criteria.MinNodes)))

	azOK := len(health.Topology.Zones) >= criteria.MinAZs
	checks = append(checks, statusCheck("AZ count", azOK, fmt.Sprintf("%d AZs (min %d)", len(health.Topology.Zones), criteria.MinAZs)))

	rfOK := health.Topology.ReplicationFactor >= criteria.ReplicationFactor
	checks = append(checks, statusCheck("RF validation", rfOK, fmt.Sprintf("RF=%d (required %d)", health.Topology.ReplicationFactor, criteria.ReplicationFactor)))

	if criteria.TolerateNodeFailure {
		ok, detail := simKindPassed(res, "node")
		checks = append(checks, statusCheck("Node failure", ok, detail))
	}
	if criteria.TolerateAZFailure {
		ok, detail := simKindPassed(res, "az")
		checks = append(checks, statusCheck("AZ failure", ok, detail))
	}

	tabOK := health.Tablets.TabletImbalance == domain.CheckPass
	if !criteria.BalancedTablets {
		tabOK = true
	}
	checks = append(checks, statusCheck("Tablet distribution", tabOK, string(health.Tablets.TabletImbalance)))

	leadOK := health.Tablets.LeaderImbalance == domain.CheckPass
	if !criteria.BalancedLeaders {
		leadOK = true
	}
	checks = append(checks, statusCheck("Leader distribution", leadOK, string(health.Tablets.LeaderImbalance)))

	if criteria.MaxP99YSQLMS > 0 {
		p99 := health.Performance.P99YSQLMS
		ok := p99 > 0 && p99 <= criteria.MaxP99YSQLMS
		st := domain.CheckPass
		if !ok {
			st = domain.CheckWarn
			if p99 == 0 {
				st = domain.CheckWarn
			}
		}
		detail := fmt.Sprintf("P99=%.0fms (requirement <%0.fms)", p99, criteria.MaxP99YSQLMS)
		if p99 == 0 {
			detail = "P99 not sampled"
		}
		checks = append(checks, domain.POCCheck{Name: fmt.Sprintf("P99 < %.0fms requirement", criteria.MaxP99YSQLMS), Status: st, Detail: detail})
	}

	if criteria.MinTPS > 0 {
		ok := health.Snapshot.Workload.TPS >= criteria.MinTPS
		st := domain.CheckPass
		if !ok {
			st = domain.CheckWarn
		}
		checks = append(checks, domain.POCCheck{
			Name:   fmt.Sprintf("%.0f TPS requirement", criteria.MinTPS),
			Status: st,
			Detail: fmt.Sprintf("observed %.0f TPS", health.Snapshot.Workload.TPS),
		})
	}

	passed := 0
	for _, c := range checks {
		if c.Status == domain.CheckPass {
			passed++
		}
	}
	return &domain.POCReport{
		Criteria:   criteria,
		Workload:   health.Snapshot.Workload,
		Nodes:      health.Topology.TServersTotal,
		Regions:    len(health.Topology.Regions),
		AZs:        len(health.Topology.Zones),
		RF:         health.Topology.ReplicationFactor,
		Checks:     checks,
		Passed:     passed,
		Total:      len(checks),
		ResultLine: fmt.Sprintf("%d/%d acceptance criteria passed.", passed, len(checks)),
	}
}

func statusCheck(name string, ok bool, detail string) domain.POCCheck {
	st := domain.CheckPass
	if !ok {
		st = domain.CheckFail
	}
	return domain.POCCheck{Name: name, Status: st, Detail: detail}
}

func simKindPassed(res *domain.ResilienceReport, kind string) (bool, string) {
	seen := false
	for _, sim := range res.Simulations {
		if sim.Kind != kind {
			continue
		}
		seen = true
		if sim.Status == domain.CheckFail {
			return false, sim.Reason
		}
	}
	if !seen {
		return false, "no " + kind + " simulations"
	}
	return true, "quorum retained"
}
