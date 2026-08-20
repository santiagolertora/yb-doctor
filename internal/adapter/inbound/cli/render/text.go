// Package render writes diagnostic reports to an io.Writer.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// Options control color and encoding.
type Options struct {
	Format  string
	NoColor bool
}

const (
	colReset  = "\033[0m"
	colBold   = "\033[1m"
	colDim    = "\033[2m"
	colRed    = "\033[31m"
	colYellow = "\033[33m"
	colGreen  = "\033[32m"
	rule      = "─────────────────────────────────────────────"
)

// Health writes an analyze report.
func Health(w io.Writer, r *domain.HealthReport, opt Options) error {
	if opt.Format == "json" {
		return encodeJSON(w, r)
	}
	c := colors(opt.NoColor)
	b := &strings.Builder{}
	heading(b, c, "YugabyteDB Cluster Health")
	printf(b, "Nodes                    %d\n", r.Topology.Nodes)
	printf(b, "Masters                  %d/%d healthy\n", r.Topology.MastersHealthy, r.Topology.MastersTotal)
	printf(b, "TServers                 %d/%d healthy\n", r.Topology.TServersHealthy, r.Topology.TServersTotal)
	printf(b, "Replication Factor       %d\n", r.Topology.ReplicationFactor)
	if len(r.Topology.Regions) > 0 {
		printf(b, "Regions                  %s\n", strings.Join(r.Topology.Regions, ", "))
	}
	if line := loadBalancerLine(r.Snapshot.LoadBalancer); line != "" {
		printf(b, "Load balancer            %s\n", line)
	}

	heading(b, c, "Tablet Health")
	printf(b, "Tablets                  %s\n", commas(r.Tablets.Total))
	printf(b, "Under-replicated         %d\n", r.Tablets.UnderReplicated)
	printf(b, "Leader imbalance         %s\n", healthStatus(c, r.Tablets.LeaderImbalance))
	printf(b, "Tablet imbalance         %s\n", healthStatus(c, r.Tablets.TabletImbalance))

	heading(b, c, "Node Distribution")
	printf(b, "%-18s %8s %11s %8s %8s %8s %6s\n", "", "Leaders", "Followers", "Total", "SST", "Pending", "Debt")
	for _, n := range r.Tablets.PerNode {
		printf(b, "%-18s %8s %11s %8s %8s %8s %6s\n",
			n.Name, commas(n.Leaders), commas(n.Followers), commas(n.Total),
			bytesShort(n.SSTBytes), bytesShort(n.PendingCompactionBytes), debtPct(n.PendingCompactionBytes, n.SSTBytes))
	}
	if r.Tablets.LeaderImbalance == domain.CheckWarn && r.Tablets.HottestNode != "" {
		printf(b, "\n%sWARNING: Leader imbalance detected%s\n", c.yellow, c.reset)
		if math.IsInf(r.Tablets.LeaderRatio, 1) {
			printf(b, "  %s holds every Raft leader; %s has none.\n", r.Tablets.HottestNode, r.Tablets.ColdestNode)
		} else {
			printf(b, "  %s has %.1fx more leaders than %s.\n", r.Tablets.HottestNode, r.Tablets.LeaderRatio, r.Tablets.ColdestNode)
		}
	}
	if r.Tablets.SSTImbalance == domain.CheckWarn && r.Tablets.SSTHottestNode != "" {
		printf(b, "\n%sWARNING: SST imbalance detected%s\n", c.yellow, c.reset)
		if math.IsInf(r.Tablets.SSTRatio, 1) {
			printf(b, "  %s holds the SST files; %s has none.\n", r.Tablets.SSTHottestNode, r.Tablets.SSTColdestNode)
		} else {
			printf(b, "  %s has %.1fx more SST bytes than %s.\n", r.Tablets.SSTHottestNode, r.Tablets.SSTRatio, r.Tablets.SSTColdestNode)
		}
	}

	heading(b, c, "Raft")
	printf(b, "Leaderless tablets       %d\n", r.Raft.Leaderless)
	printf(b, "Under-replicated         %d\n", r.Raft.UnderReplicated)
	printf(b, "Slow followers           %d\n", r.Raft.SlowFollowers)

	heading(b, c, "Performance")
	printf(b, "P99 YSQL latency         %s\n", p99Line(r.Performance))
	printf(b, "Slow queries             %d\n", r.Performance.SlowQueries)
	printf(b, "Hot tablets              %d\n", r.Performance.HotTablets)
	printf(b, "Compaction pressure      %s\n", r.Performance.CompactionPressure)

	heading(b, c, "Diagnosis")
	if len(r.Findings) == 0 {
		printf(b, "\n%sNo issues detected.%s\n", c.green, c.reset)
	}
	for _, f := range r.Findings {
		printf(b, "\n%s[%s]%s %s\n", sevColor(c, f.Severity), f.Severity, c.reset, f.Title)
		for _, ev := range f.Evidence {
			printf(b, "       → %s\n", ev)
		}
	}
	printf(b, "\nOverall score: %s%d/100%s\n", scoreColor(c, r.Score), r.Score, c.reset)
	if r.Diff != nil {
		writeDiff(b, c, r.Diff)
	}
	return writeReport(w, b.String())
}

// Changes writes a before/after block for --diff / --watch.
func Changes(w io.Writer, d *domain.HealthDiff, opt Options) error {
	if d == nil {
		return nil
	}
	if opt.Format == "json" {
		return encodeJSON(w, d)
	}
	c := colors(opt.NoColor)
	b := &strings.Builder{}
	writeDiff(b, c, d)
	return writeReport(w, b.String())
}

// Resilience writes a failure-domain report.
func Resilience(w io.Writer, r *domain.ResilienceReport, opt Options) error {
	if opt.Format == "json" {
		return encodeJSON(w, r)
	}
	c := colors(opt.NoColor)
	b := &strings.Builder{}
	heading(b, c, "Topology")
	b.WriteString("\n")
	for _, region := range r.TopologyTree {
		printf(b, "        %s\n", region.Name)
		if len(region.Zones) == 0 {
			continue
		}
		printf(b, "       %s\n", azHeader(region.Zones))
		printf(b, "        %s\n", azNodes(region.Zones))
	}
	printf(b, "\nRF=%d\n", r.Snapshot.ReplicationFactor)

	heading(b, c, "Failure simulations")
	for _, sim := range r.Simulations {
		printf(b, "\n%s:\n%s%s%s\n", sim.Name, statusColor(c, sim.Status), sim.Status, c.reset)
		if sim.Status == domain.CheckFail {
			printf(b, "%s\n", sim.Reason)
		}
	}
	printf(b, "\nRPO:\n%s\n", r.RPO)
	printf(b, "\nRecommendation:\n%s\n", r.Recommendation)
	return writeReport(w, b.String())
}

// Explain writes an explanation report.
func Explain(w io.Writer, e *domain.Explanation, opt Options) error {
	if opt.Format == "json" {
		return encodeJSON(w, e)
	}
	c := colors(opt.NoColor)
	b := &strings.Builder{}
	heading(b, c, strings.ToUpper(string(e.Code)))
	section(b, c, "WHAT")
	printf(b, "\n%s\n", e.What)
	section(b, c, "WHY IT MATTERS")
	printf(b, "\n%s\n", e.WhyItMatters)
	section(b, c, "CURRENT CLUSTER")
	b.WriteString("\n")
	for _, line := range e.CurrentCluster {
		printf(b, "%s\n", line)
	}
	section(b, c, "POSSIBLE CAUSES")
	b.WriteString("\n")
	for _, line := range e.PossibleCauses {
		printf(b, "• %s\n", line)
	}
	section(b, c, "NEXT STEPS")
	b.WriteString("\n")
	for i, line := range e.NextSteps {
		printf(b, "%d. %s\n", i+1, line)
	}
	return writeReport(w, b.String())
}

// POC writes a proof-of-concept readiness report.
func POC(w io.Writer, r *domain.POCReport, opt Options) error {
	if opt.Format == "json" {
		return encodeJSON(w, r)
	}
	c := colors(opt.NoColor)
	b := &strings.Builder{}
	heading(b, c, "YUGABYTEDB POC REPORT")
	section(b, c, "Workload")
	printf(b, "Database size       %s\n", bytesHuman(r.Workload.DatabaseBytes))
	printf(b, "TPS                 %s\n", commas64(int64(r.Workload.TPS)))
	printf(b, "Read/write          %.0f/%.0f\n", r.Workload.ReadPct, r.Workload.WritePct)
	printf(b, "Connections         %d\n", r.Workload.Connections)
	section(b, c, "Architecture")
	printf(b, "Nodes               %d\n", r.Nodes)
	printf(b, "Regions             %d\n", r.Regions)
	printf(b, "AZs                 %d\n", r.AZs)
	printf(b, "RF                  %d\n", r.RF)
	section(b, c, "Validation")
	for _, chk := range r.Checks {
		printf(b, "[%s] %s\n", status(c, chk.Status), chk.Name)
	}
	section(b, c, "POC RESULT")
	printf(b, "\n%s\n", r.ResultLine)
	return writeReport(w, b.String())
}

type palette struct {
	reset, bold, dim, red, yellow, green string
}

func colors(off bool) palette {
	if off {
		return palette{}
	}
	return palette{reset: colReset, bold: colBold, dim: colDim, red: colRed, yellow: colYellow, green: colGreen}
}

func heading(b *strings.Builder, c palette, title string) {
	printf(b, "\n%s%s%s\n%s\n", c.bold, title, c.reset, rule)
}

func section(b *strings.Builder, c palette, title string) {
	printf(b, "\n%s%s%s\n%s\n", c.bold, title, c.reset, strings.Repeat("─", 21))
}

func status(c palette, s domain.CheckStatus) string {
	return statusColor(c, s) + string(s) + c.reset
}

func healthStatus(c palette, s domain.CheckStatus) string {
	label := string(s)
	switch s {
	case domain.CheckPass:
		label = "OK"
	case domain.CheckWarn:
		label = "WARNING"
	}
	return statusColor(c, s) + label + c.reset
}

func statusColor(c palette, s domain.CheckStatus) string {
	switch s {
	case domain.CheckPass:
		return c.green
	case domain.CheckFail:
		return c.red
	default:
		return c.yellow
	}
}

func sevColor(c palette, s domain.Severity) string {
	switch s {
	case domain.SeverityHigh:
		return c.red
	case domain.SeverityMedium:
		return c.yellow
	default:
		return c.dim
	}
}

func scoreColor(c palette, score int) string {
	switch {
	case score >= 90:
		return c.green
	case score >= 70:
		return c.yellow
	default:
		return c.red
	}
}

func commas(n int) string { return commas64(int64(n)) }

func commas64(n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return sign + s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return sign + strings.Join(parts, ",")
}

func ms(v float64) string {
	if v == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0fms", v)
}

func p99Line(p domain.PerformanceSummary) string {
	s := ms(p.P99YSQLMS)
	if p.P99Source != "" && p.P99YSQLMS > 0 {
		return fmt.Sprintf("%s (%s)", s, p.P99Source)
	}
	return s
}

func loadBalancerLine(lb domain.LoadBalancer) string {
	if !lb.Known && !lb.HasIdle {
		return ""
	}
	parts := make([]string, 0, 2)
	if lb.Known {
		if lb.Enabled {
			parts = append(parts, "enabled")
		} else {
			parts = append(parts, "disabled")
		}
	}
	switch {
	case lb.HasIdle && lb.Idle:
		parts = append(parts, "idle")
	case lb.HasIdle && !lb.Idle:
		parts = append(parts, "still running")
	case lb.Known && lb.Enabled:
		parts = append(parts, "idle unknown")
	}
	return strings.Join(parts, ", ")
}

func writeDiff(b *strings.Builder, c palette, d *domain.HealthDiff) {
	heading(b, c, "Changes since "+d.Baseline)
	printf(b, "Score                   %d → %d\n", d.ScoreFrom, d.ScoreTo)
	if d.MastersFrom != d.MastersTo {
		printf(b, "Masters                 %s → %s\n", d.MastersFrom, d.MastersTo)
	}
	if d.TServersFrom != d.TServersTo {
		printf(b, "TServers                %s → %s\n", d.TServersFrom, d.TServersTo)
	}
	if d.UnderReplicatedFrom != d.UnderReplicatedTo {
		printf(b, "Under-replicated        %d → %d\n", d.UnderReplicatedFrom, d.UnderReplicatedTo)
	}
	if d.LeaderlessFrom != d.LeaderlessTo {
		printf(b, "Leaderless              %d → %d\n", d.LeaderlessFrom, d.LeaderlessTo)
	}
	if d.P99From != d.P99To {
		printf(b, "P99                     %s → %s\n", ms(d.P99From), ms(d.P99To))
	}
	for _, n := range d.Leaders {
		printf(b, "%-22s %d → %d leaders\n", n.Name, n.From, n.To)
	}
	if len(d.FindingsAdded) > 0 {
		printf(b, "Findings added          %s\n", joinCodes(d.FindingsAdded))
	}
	if len(d.FindingsRemoved) > 0 {
		printf(b, "Findings gone           %s\n", joinCodes(d.FindingsRemoved))
	}
}

func joinCodes(codes []domain.FindingCode) string {
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, string(c))
	}
	return strings.Join(parts, ", ")
}

func bytesShort(n int64) string {
	if n <= 0 {
		return "n/a"
	}
	const gi = 1 << 30
	const mi = 1 << 20
	if n >= gi {
		v := float64(n) / float64(gi)
		if v >= 10 {
			return fmt.Sprintf("%.0fG", v)
		}
		return fmt.Sprintf("%.1fG", v)
	}
	if n >= mi {
		return fmt.Sprintf("%.0fM", float64(n)/float64(mi))
	}
	if n >= 10<<10 {
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func debtPct(pending, sst int64) string {
	if sst <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", float64(pending)/float64(sst)*100)
}

func bytesHuman(n int64) string {
	if n <= 0 {
		return "n/a"
	}
	const tb = 1_000_000_000_000
	const gb = 1_000_000_000
	if n >= tb {
		return fmt.Sprintf("%.1f TB", float64(n)/float64(tb))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
}

func azHeader(zones []domain.AZBranch) string {
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		names = append(names, z.Name)
	}
	return strings.Join(names, "   |   ")
}

func azNodes(zones []domain.AZBranch) string {
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		if len(z.Nodes) == 0 {
			names = append(names, "-")
			continue
		}
		names = append(names, strings.Join(z.Nodes, ","))
	}
	return strings.Join(names, "      ")
}

func printf(b *strings.Builder, format string, args ...any) {
	fmt.Fprintf(b, format, args...)
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func writeReport(w io.Writer, s string) error {
	if _, err := io.WriteString(w, s); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
