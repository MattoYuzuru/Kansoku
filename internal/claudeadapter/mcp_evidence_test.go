package claudeadapter_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/claudeadapter"
)

func TestClaudeMCPConnectionMapsToGenericMetadataOnlyFrame(t *testing.T) {
	duration := int64(23)
	frame, err := claudeadapter.MapMCPConnectionEvidence(claudeadapter.ClaudeMCPConnectionMetadata{
		ServerID: "srv_opaque", ServerName: "approved-alias",
		AgentInstallationID: "ain_claude", SourceInstanceID: "src_claude_mcp",
		AttemptID: "attempt_1", State: "connected", Transport: "stdio",
		DurationMS: &duration, ObservedAt: time.Unix(100, 0).UTC(),
		AdapterVersion: "2.1.197", SchemaVersion: "claude.mcp/1", IdempotencyKey: "idem_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Kind != "connection" || frame.ServerID != "srv_opaque" || frame.State != "connected" {
		t.Fatalf("unexpected generic frame: %+v", frame)
	}
	typ := reflect.TypeOf(frame)
	for _, prohibited := range []string{"argument", "result", "error_text", "url", "command", "environment", "credential", "resource_uri"} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.Contains(strings.ToLower(typ.Field(i).Name), prohibited) {
				t.Fatalf("generic frame exposes prohibited field %q", typ.Field(i).Name)
			}
		}
	}
}

func TestClaudeMCPRedactedIdentityStaysNotObserved(t *testing.T) {
	if _, err := claudeadapter.MapMCPConnectionEvidence(claudeadapter.ClaudeMCPConnectionMetadata{IdentityRedacted: true}); err == nil {
		t.Fatal("redacted third-party identity must not be guessed")
	}
}
