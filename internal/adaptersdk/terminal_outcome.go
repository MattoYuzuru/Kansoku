package adaptersdk

import "strings"

// TerminalOutcome is the agent-independent, metadata-only classification of
// one native terminal status. FailureClass is retained by contours that have
// a dedicated safe slot; canonical event lanes always use Outcome.
type TerminalOutcome struct {
	Outcome      string
	FailureClass string
	Terminal     bool
}

// ClassifyTerminalStatus implements terminal-outcomes/1. Unknown and missing
// statuses never default to failure.
func ClassifyTerminalStatus(status string, resultError bool) TerminalOutcome {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if resultError && (normalized == "completed" || normalized == "success" ||
		normalized == "succeeded" || normalized == "ok") {
		return TerminalOutcome{
			Outcome: "failed", FailureClass: "execution", Terminal: true,
		}
	}
	switch normalized {
	case "completed", "success", "succeeded", "ok":
		return TerminalOutcome{
			Outcome: "succeeded", FailureClass: "none", Terminal: true,
		}
	case "failure", "failed", "error", "execution_error":
		return TerminalOutcome{
			Outcome: "failed", FailureClass: "execution", Terminal: true,
		}
	case "protocol_error", "json_rpc_error":
		return TerminalOutcome{
			Outcome: "failed", FailureClass: "protocol", Terminal: true,
		}
	case "declined", "denied", "policy_denial":
		return TerminalOutcome{
			Outcome: "failed", FailureClass: "policy_denial", Terminal: true,
		}
	case "cancelled", "canceled":
		return TerminalOutcome{
			Outcome: "cancelled", FailureClass: "cancelled", Terminal: true,
		}
	case "timed_out", "timeout":
		return TerminalOutcome{
			Outcome: "timed_out", FailureClass: "timeout", Terminal: true,
		}
	case "interrupted":
		return TerminalOutcome{
			Outcome: "interrupted", FailureClass: "transport_loss", Terminal: true,
		}
	case "abandoned", "transport_lost":
		return TerminalOutcome{
			Outcome: "abandoned", FailureClass: "transport_loss", Terminal: true,
		}
	case "":
		return TerminalOutcome{
			Outcome: "unknown", FailureClass: "missing_terminal", Terminal: false,
		}
	default:
		return TerminalOutcome{
			Outcome: "unknown", FailureClass: "unknown", Terminal: true,
		}
	}
}
