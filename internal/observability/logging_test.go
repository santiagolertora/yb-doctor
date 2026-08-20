package observability

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewLoggerLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level string
		want  slog.Level
	}{
		{level: "debug", want: slog.LevelDebug},
		{level: "info", want: slog.LevelInfo},
		{level: "warn", want: slog.LevelWarn},
		{level: "error", want: slog.LevelError},
		{level: "unknown", want: slog.LevelInfo},
	}
	for _, tc := range tests {
		t.Run(tc.level, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := NewLogger(&buf, tc.level)
			require.NotNil(t, logger)
			logger.Info("hello", "k", "v")
			if tc.want <= slog.LevelInfo {
				require.Contains(t, buf.String(), "hello")
			}
		})
	}
}
