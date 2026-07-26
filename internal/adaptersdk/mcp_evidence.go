package adaptersdk

import "time"

// MCPEvidenceFrame is the generic, adapter-owned handoff shape for MCP
// inventory, connection and call evidence. The closed shape intentionally
// cannot carry arguments, results, error text, URLs, commands, environment
// values, credentials or resource URIs.
type MCPEvidenceFrame struct {
	Kind                string    `json:"kind"`
	ServerID            string    `json:"server_id"`
	ServerName          string    `json:"server_name"`
	ToolID              string    `json:"tool_id,omitempty"`
	ToolName            string    `json:"tool_name,omitempty"`
	AgentInstallationID string    `json:"agent_installation_id"`
	SessionID           string    `json:"session_id,omitempty"`
	SourceInstanceID    string    `json:"source_instance_id"`
	State               string    `json:"state,omitempty"`
	Scope               string    `json:"scope,omitempty"`
	Transport           string    `json:"transport,omitempty"`
	Locality            string    `json:"locality,omitempty"`
	Configured          *bool     `json:"configured,omitempty"`
	Enabled             *bool     `json:"enabled,omitempty"`
	Completeness        string    `json:"completeness,omitempty"`
	LogicalCallID       string    `json:"logical_call_id,omitempty"`
	AttemptID           string    `json:"attempt_id,omitempty"`
	DurationMS          *int64    `json:"duration_ms,omitempty"`
	FailureClass        string    `json:"failure_class,omitempty"`
	ApprovalDecision    string    `json:"approval_decision,omitempty"`
	ApprovalSource      string    `json:"approval_source,omitempty"`
	PageNumber          int       `json:"page_number,omitempty"`
	Revision            string    `json:"revision,omitempty"`
	ObservedAt          time.Time `json:"observed_at"`
	AdapterVersion      string    `json:"adapter_version"`
	SchemaVersion       string    `json:"schema_version"`
	IdempotencyKey      string    `json:"idempotency_key"`
}
