package observability

import (
	"testing"

	"kansoku.local/kansoku/internal/claudeadapter"
)

func TestClaude2197SanitizedTerminalStatusMatrix(t *testing.T) {
	tests := []struct {
		status  string
		outcome string
	}{
		{"success", "succeeded"},
		{"failure", "failed"},
		{"cancelled", "cancelled"},
		{"denied", "failed"},
		{"timed_out", "timed_out"},
		{"interrupted", "interrupted"},
		{"", "unknown"},
		{"future_terminal", "unknown"},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			fields := claudeHookSafeFields(claudeadapter.HookHelperOutput{
				EventID: "fixture-event", SessionID: "fixture-session",
				ObservedAt: "2026-07-30T00:00:00Z", EventType: "tool.called",
				ToolID: "fixture-tool", ToolStatus: test.status,
			})
			if fields["outcome"] != test.outcome {
				t.Fatalf("outcome=%v want %s", fields["outcome"], test.outcome)
			}
		})
	}
}
