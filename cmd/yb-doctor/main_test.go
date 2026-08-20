package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunVersion(t *testing.T) {
	t.Parallel()
	code := run([]string{"version"})
	require.Equal(t, 0, code)
}
