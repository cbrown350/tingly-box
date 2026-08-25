package visionproxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVisionMaxTokens(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		t.Setenv(visionMaxTokensEnv, "")
		assert.Equal(t, int64(defaultVisionMaxTokens), visionMaxTokens())
	})

	t.Run("environment overrides", func(t *testing.T) {
		t.Setenv(visionMaxTokensEnv, "4096")
		assert.Equal(t, int64(4096), visionMaxTokens())
	})

	t.Run("surrounding whitespace tolerated", func(t *testing.T) {
		t.Setenv(visionMaxTokensEnv, "  2048\n")
		assert.Equal(t, int64(2048), visionMaxTokens())
	})

	// A bad value must not silently become a tiny budget — that is the very
	// failure this knob exists to prevent.
	for _, bad := range []string{"nonsense", "0", "-1", "12.5"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv(visionMaxTokensEnv, bad)
			assert.Equal(t, int64(defaultVisionMaxTokens), visionMaxTokens())
		})
	}
}

// The default has to clear the reasoning preamble of a reasoning model.
// Measured: qwen3.5:397b yields zero content tokens at 256 on every attempt.
func TestDefaultVisionMaxTokens_ClearsReasoningPreamble(t *testing.T) {
	assert.GreaterOrEqual(t, defaultVisionMaxTokens, 1024,
		"a budget this small strips images instead of describing them")
}

func TestErrTruncatedBeforeContent_IsActionable(t *testing.T) {
	err := errTruncatedBeforeContent("qwen3.5:397b", 256)
	require.Error(t, err)
	msg := err.Error()
	// Name the model, the budget that was exhausted, and the knob to turn.
	assert.Contains(t, msg, "qwen3.5:397b")
	assert.Contains(t, msg, "256")
	assert.Contains(t, msg, visionMaxTokensEnv)
	assert.True(t, strings.Contains(msg, "no") || strings.Contains(msg, "before"),
		"must convey that nothing was written, not that the model failed")
}
