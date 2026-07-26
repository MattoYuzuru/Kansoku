package claudeadapter

import (
	"errors"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// ClaudeMCPConnectionMetadata is the closed, default-safe subset an active
// Claude source may supply. Exact server identity is required; when Claude
// redacts it the contour remains not_observed rather than being guessed.
type ClaudeMCPConnectionMetadata struct {
	ServerID            string
	ServerName          string
	AgentInstallationID string
	SessionID           string
	SourceInstanceID    string
	AttemptID           string
	State               string
	Transport           string
	DurationMS          *int64
	SafeFailureClass    string
	ObservedAt          time.Time
	AdapterVersion      string
	SchemaVersion       string
	IdempotencyKey      string
	IdentityRedacted    bool
}

func MapMCPConnectionEvidence(in ClaudeMCPConnectionMetadata) (adaptersdk.MCPEvidenceFrame, error) {
	if in.IdentityRedacted || in.ServerID == "" || in.ServerName == "" {
		return adaptersdk.MCPEvidenceFrame{}, errors.New("claude_mcp_identity_not_observed")
	}
	return adaptersdk.MCPEvidenceFrame{
		Kind:                "connection",
		ServerID:            in.ServerID,
		ServerName:          in.ServerName,
		AgentInstallationID: in.AgentInstallationID,
		SessionID:           in.SessionID,
		SourceInstanceID:    in.SourceInstanceID,
		AttemptID:           in.AttemptID,
		State:               in.State,
		Transport:           in.Transport,
		DurationMS:          in.DurationMS,
		FailureClass:        in.SafeFailureClass,
		ObservedAt:          in.ObservedAt,
		AdapterVersion:      in.AdapterVersion,
		SchemaVersion:       in.SchemaVersion,
		IdempotencyKey:      in.IdempotencyKey,
	}, nil
}
