package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

func TestExplainAllCatalogCodes(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{snap: sixNodeImbalance()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	for _, code := range KnownFindingCodes() {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			exp, err := svc.Explain(t.Context(), code)
			require.NoError(t, err)
			require.NotEmpty(t, exp.What)
			require.NotEmpty(t, exp.WhyItMatters)
			require.NotEmpty(t, exp.NextSteps)
		})
	}
}

func TestExplainWithoutCollectorErrorStillReturnsArticle(t *testing.T) {
	t.Parallel()
	svc, err := New(testCfg(t), staticCollector{err: errBoom{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	exp, err := svc.Explain(t.Context(), string(domain.CodeUnderReplicated))
	require.NoError(t, err)
	require.Contains(t, exp.CurrentCluster[0], "no cluster snapshot")
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
