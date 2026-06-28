package main

import (
	"testing"

	namedtypes "github.com/gomatic/yze-go-namedtypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

func TestMainRunsTheAnalyzer(t *testing.T) {
	original := run
	t.Cleanup(func() { run = original })

	var got *analysis.Analyzer
	run = func(a *analysis.Analyzer) { got = a }

	main()

	require.NotNil(t, got)
	assert.Same(t, namedtypes.Analyzer, got)
}
