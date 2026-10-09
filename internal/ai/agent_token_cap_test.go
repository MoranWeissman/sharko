package ai

import (
	"testing"
)

// TestAgentMaxOutputTokens asserts the package constant value so a future edit
// can't silently lower the cap below 4096 again (V2-cleanup-43).
func TestAgentMaxOutputTokens(t *testing.T) {
	const want = 4096
	if agentMaxOutputTokens != want {
		t.Errorf("agentMaxOutputTokens = %d; want %d", agentMaxOutputTokens, want)
	}
}
