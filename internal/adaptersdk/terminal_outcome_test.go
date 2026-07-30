package adaptersdk

import "testing"

func TestTerminalOutcomeContractMatrix(t *testing.T) {
	tests := []struct {
		status       string
		resultError  bool
		outcome      string
		failureClass string
		terminal     bool
	}{
		{"completed", false, "succeeded", "none", true},
		{"completed", true, "failed", "execution", true},
		{"failed", false, "failed", "execution", true},
		{"protocol_error", false, "failed", "protocol", true},
		{"declined", false, "failed", "policy_denial", true},
		{"cancelled", false, "cancelled", "cancelled", true},
		{"timed_out", false, "timed_out", "timeout", true},
		{"interrupted", false, "interrupted", "transport_loss", true},
		{"", false, "unknown", "missing_terminal", false},
		{"future_terminal", false, "unknown", "unknown", true},
	}
	for _, test := range tests {
		t.Run(test.status+"/"+test.failureClass, func(t *testing.T) {
			got := ClassifyTerminalStatus(test.status, test.resultError)
			if got.Outcome != test.outcome ||
				got.FailureClass != test.failureClass ||
				got.Terminal != test.terminal {
				t.Fatalf("classification=%+v", got)
			}
		})
	}
}
