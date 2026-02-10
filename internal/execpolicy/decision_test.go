package execpolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecision_String(t *testing.T) {
	assert.Equal(t, "allow", DecisionAllow.String())
	assert.Equal(t, "prompt", DecisionPrompt.String())
	assert.Equal(t, "forbidden", DecisionForbidden.String())
}

func TestParseDecision(t *testing.T) {
	tests := []struct {
		input    string
		expected Decision
		wantErr  bool
	}{
		{"allow", DecisionAllow, false},
		{"Allow", DecisionAllow, false},
		{"ALLOW", DecisionAllow, false},
		{"prompt", DecisionPrompt, false},
		{"Prompt", DecisionPrompt, false},
		{"forbidden", DecisionForbidden, false},
		{"Forbidden", DecisionForbidden, false},
		{"invalid", DecisionAllow, true},
		{"", DecisionAllow, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := ParseDecision(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, d)
			}
		})
	}
}

func TestDecision_Max(t *testing.T) {
	assert.Equal(t, DecisionAllow, DecisionAllow.Max(DecisionAllow))
	assert.Equal(t, DecisionPrompt, DecisionAllow.Max(DecisionPrompt))
	assert.Equal(t, DecisionForbidden, DecisionAllow.Max(DecisionForbidden))
	assert.Equal(t, DecisionPrompt, DecisionPrompt.Max(DecisionAllow))
	assert.Equal(t, DecisionPrompt, DecisionPrompt.Max(DecisionPrompt))
	assert.Equal(t, DecisionForbidden, DecisionPrompt.Max(DecisionForbidden))
	assert.Equal(t, DecisionForbidden, DecisionForbidden.Max(DecisionAllow))
	assert.Equal(t, DecisionForbidden, DecisionForbidden.Max(DecisionPrompt))
	assert.Equal(t, DecisionForbidden, DecisionForbidden.Max(DecisionForbidden))
}

func TestDecision_Ordering(t *testing.T) {
	assert.True(t, DecisionAllow < DecisionPrompt)
	assert.True(t, DecisionPrompt < DecisionForbidden)
}
