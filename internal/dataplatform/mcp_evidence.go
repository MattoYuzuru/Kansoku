package dataplatform

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

var safeMCPIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@|-]{0,127}$`)

// MCPEvidenceFrame remains exported here as a compatibility alias for the
// CLI and live-canary callers. The owning contract lives in adaptersdk so
// every adapter maps into one agent-independent frame.
type MCPEvidenceFrame = adaptersdk.MCPEvidenceFrame

func PersistMCPEvidence(ctx context.Context, pool *pgxpool.Pool, f MCPEvidenceFrame) error {
	if pool == nil || !safeMCPIdentity.MatchString(f.ServerID) || !safeMCPIdentity.MatchString(f.ServerName) ||
		!safeMCPIdentity.MatchString(f.AgentInstallationID) || !safeMCPIdentity.MatchString(f.SourceInstanceID) ||
		f.ObservedAt.IsZero() || f.AdapterVersion == "" || f.SchemaVersion == "" || f.IdempotencyKey == "" {
		return errors.New("invalid_mcp_evidence")
	}
	if f.ToolID != "" && (!safeMCPIdentity.MatchString(f.ToolID) || !safeMCPIdentity.MatchString(f.ToolName)) {
		return errors.New("invalid_mcp_evidence")
	}
	adapterID := "mcp-evidence"
	if err := EnsureDimensions(ctx, pool, DimensionRefs{
		DeviceID: "dev_" + f.AgentInstallationID, AgentInstallationID: f.AgentInstallationID, AgentID: adapterID,
		SurfaceID: "srf_" + f.AgentInstallationID, ProjectID: "prj_" + f.AgentInstallationID,
		SessionID: f.SessionID, ComponentID: f.ServerID, ComponentKind: "mcp",
		AdapterVersionID: "av_" + f.SourceInstanceID, AdapterID: adapterID, AdapterVersion: f.AdapterVersion,
		SourceInstanceID: f.SourceInstanceID, SourceKind: "evidence_bridge",
	}); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE components SET declared_name=coalesce(declared_name,$2) WHERE component_id=$1`, f.ServerID, f.ServerName); err != nil {
		return err
	}
	if f.ToolID != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO components(component_id,kind,declared_name) VALUES($1,'command',$2) ON CONFLICT(component_id) DO NOTHING`, f.ToolID, f.ToolName); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `INSERT INTO component_relations(relation_id,parent_id,child_id,relation_kind) VALUES($1,$2,$3,'bundles') ON CONFLICT(relation_id) DO NOTHING`,
			handoffID("mcp-relation", f.ServerID, f.ToolID), f.ServerID, f.ToolID); err != nil {
			return err
		}
	}
	switch f.Kind {
	case "server":
		if f.Configured == nil || f.Enabled == nil || !oneOf(f.Scope, "user", "system", "repository", "managed", "unknown") || !validMCPTransport(f.Transport) || !oneOf(f.Locality, "local", "remote", "unknown") || !validCompleteness(f.Completeness) {
			return errors.New("invalid_mcp_server_evidence")
		}
		_, err := pool.Exec(ctx, `INSERT INTO mcp_server_observations(
			server_observation_id,server_component_id,agent_installation_id,source_instance_id,scope,configured,enabled,transport,locality,
			configuration_fingerprint,protocol_version_state,server_version_state,enumeration_completeness,observed_at,adapter_version,schema_version,idempotency_key)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'not_observed','not_observed',$11,$12,$13,$14,$15)
			ON CONFLICT(source_instance_id,idempotency_key) DO NOTHING`,
			handoffID("mcp-server", f.SourceInstanceID, f.IdempotencyKey), f.ServerID, f.AgentInstallationID, f.SourceInstanceID, f.Scope, *f.Configured, *f.Enabled, f.Transport, f.Locality,
			handoffID("mcp-config", f.ServerID, f.Revision), f.Completeness, f.ObservedAt, f.AdapterVersion, f.SchemaVersion, f.IdempotencyKey)
		return err
	case "primitive":
		if f.ToolID == "" || f.PageNumber < 1 || f.Revision == "" || !validCompleteness(f.Completeness) {
			return errors.New("invalid_mcp_primitive_evidence")
		}
		_, err := pool.Exec(ctx, `INSERT INTO mcp_primitive_observations(
			primitive_observation_id,server_component_id,primitive_component_id,source_instance_id,primitive_kind,approved_display_alias,
			page_number,revision,enumeration_completeness,first_advertised_at,last_advertised_at,adapter_version,schema_version,idempotency_key)
			VALUES($1,$2,$3,$4,'tool',$5,$6,$7,$8,$9,$9,$10,$11,$12)
			ON CONFLICT(source_instance_id,idempotency_key) DO NOTHING`,
			handoffID("mcp-primitive", f.SourceInstanceID, f.IdempotencyKey), f.ServerID, f.ToolID, f.SourceInstanceID, f.ToolName, f.PageNumber, f.Revision, f.Completeness, f.ObservedAt, f.AdapterVersion, f.SchemaVersion, f.IdempotencyKey)
		return err
	case "connection":
		if !validMCPConnectionState(f.State) || !validMCPTransport(f.Transport) || f.AttemptID == "" {
			return errors.New("invalid_mcp_connection_evidence")
		}
		failure := f.FailureClass
		if failure == "" {
			failure = "none"
		}
		if !oneOf(failure, "none", "version_mismatch", "capability_negotiation", "auth", "transport", "process_exit", "timeout", "unknown") {
			return errors.New("invalid_mcp_connection_evidence")
		}
		_, err := pool.Exec(ctx, `INSERT INTO mcp_connection_assertions(
			connection_assertion_id,server_component_id,agent_installation_id,session_id,source_instance_id,attempt_id,state,observed_at,duration_ms,
			transport,failure_class,evidence_tier,confidence,adapter_version,schema_version,idempotency_key)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'native',1,$12,$13,$14)
			ON CONFLICT(source_instance_id,idempotency_key) DO NOTHING`,
			handoffID("mcp-connection", f.SourceInstanceID, f.IdempotencyKey), f.ServerID, f.AgentInstallationID, nullableString(f.SessionID), f.SourceInstanceID, f.AttemptID, f.State, f.ObservedAt, f.DurationMS, f.Transport, failure, f.AdapterVersion, f.SchemaVersion, f.IdempotencyKey)
		return err
	case "call":
		if f.ToolID == "" || f.LogicalCallID == "" || !validMCPCallState(f.State) {
			return errors.New("invalid_mcp_call_evidence")
		}
		failure := f.FailureClass
		if failure == "" {
			failure = "none"
		}
		decision := f.ApprovalDecision
		if decision == "" {
			decision = "not_observed"
		}
		source := f.ApprovalSource
		if source == "" {
			source = "not_observed"
		}
		if !oneOf(failure, "none", "json_rpc", "execution", "timeout", "cancelled", "policy_denial", "transport_loss", "missing_terminal", "contradictory_terminal", "unknown") || !oneOf(decision, "not_observed", "allowed", "denied") || !oneOf(source, "not_observed", "user", "policy", "agent", "system") {
			return errors.New("invalid_mcp_call_evidence")
		}
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO mcp_call_assertions(
				call_assertion_id,logical_call_id,server_component_id,tool_component_id,agent_installation_id,session_id,source_instance_id,state,
				observed_at,duration_ms,safe_error_class,approval_decision,approval_source,evidence_tier,confidence,adapter_version,schema_version,idempotency_key)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'native',1,$14,$15,$16)
				ON CONFLICT(source_instance_id,idempotency_key) DO NOTHING`,
				handoffID("mcp-call", f.SourceInstanceID, f.IdempotencyKey), f.LogicalCallID, f.ServerID, f.ToolID, f.AgentInstallationID, nullableString(f.SessionID), f.SourceInstanceID, f.State, f.ObservedAt, f.DurationMS, failure, decision, source, f.AdapterVersion, f.SchemaVersion, f.IdempotencyKey)
			return err
		})
	default:
		return errors.New("invalid_mcp_evidence_kind")
	}
}

func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func validMCPTransport(v string) bool {
	return oneOf(v, "stdio", "streamable_http", "sse", "websocket", "other", "unknown")
}
func validCompleteness(v string) bool { return oneOf(v, "complete", "partial", "degraded", "unknown") }
func validMCPConnectionState(v string) bool {
	return oneOf(v, "configured", "connecting", "connected", "failed", "disconnected", "timed_out", "unknown")
}
func validMCPCallState(v string) bool {
	return oneOf(v, "decided", "denied", "started", "progressing", "completed", "execution_error", "protocol_error", "cancelled", "timed_out", "transport_lost", "incomplete")
}
