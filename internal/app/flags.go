package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/santiagolertora/yb-doctor/internal/config"
	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func attachFlagEvidence(findings []domain.Finding, snap *domain.Snapshot, allow []config.FlagSpec) {
	if len(allow) == 0 {
		return
	}
	for i := range findings {
		topics := flagTopicsFor(findings[i].Code)
		if len(topics) == 0 {
			continue
		}
		lines := flagEvidence(snap, findings[i].Node, allow, topics...)
		if len(lines) == 0 {
			continue
		}
		findings[i].Evidence = append(findings[i].Evidence, lines...)
	}
}

func flagTopicsFor(code domain.FindingCode) []string {
	switch code {
	case domain.CodeCompactionPressure, domain.CodeDiskPressure:
		return []string{config.FlagTopicCompaction, config.FlagTopicMemory}
	case domain.CodeSSTImbalance:
		return []string{config.FlagTopicSplitting, config.FlagTopicStorage, config.FlagTopicMemory}
	case domain.CodeTabletImbalance, domain.CodeLeaderImbalance, domain.CodeHotTablets:
		return []string{config.FlagTopicSplitting, config.FlagTopicStorage}
	default:
		return nil
	}
}

func flagEvidence(snap *domain.Snapshot, node string, allow []config.FlagSpec, topics ...string) []string {
	rt, ok := snap.Performance.Nodes[node]
	if !ok || len(rt.Flags) == 0 {
		return nil
	}
	wanted := map[string]struct{}{}
	for _, t := range topics {
		wanted[t] = struct{}{}
	}
	out := make([]string, 0)
	for _, spec := range allow {
		if _, ok := wanted[spec.Topic]; !ok {
			continue
		}
		got, ok := rt.Flags[spec.Name]
		if !ok || !flagDiffers(got, spec.Default) {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s (default %s)", spec.Name, prettyFlagValue(got), prettyFlagValue(spec.Default)))
	}
	return out
}

func flagDiffers(got, def string) bool {
	g := strings.TrimSpace(got)
	d := strings.TrimSpace(def)
	if strings.EqualFold(g, d) {
		return false
	}
	gf, gerr := strconv.ParseFloat(g, 64)
	df, derr := strconv.ParseFloat(d, 64)
	if gerr == nil && derr == nil {
		return math.Abs(gf-df) > 0.001
	}
	return true
}

func prettyFlagValue(v string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return v
	}
	if f == float64(int64(f)) && !strings.Contains(v, ".") {
		return v
	}
	return strconv.FormatFloat(f, 'g', 4, 64)
}

func formatBytesIEC(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1<<30 {
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
}
