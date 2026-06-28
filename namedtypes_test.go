package namedtypes_test

import (
	"testing"

	namedtypes "github.com/gomatic/yze-go-namedtypes"
	"github.com/stretchr/testify/assert"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestBarePrimitiveParameterIsReported(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), namedtypes.Analyzer, "a")
}

func TestRegistrationIsWellFormed(t *testing.T) {
	assert.NoError(t, namedtypes.Registration.Validate())
	assert.Equal(t, "yze/go/namedtypes", namedtypes.Registration.RuleID())
	assert.Same(t, namedtypes.Analyzer, namedtypes.Registration.Analyzer)
}
