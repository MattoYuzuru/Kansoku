package fakebridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

func TestDifferentlyShapedBridgeUsesGenericContractWithoutCoreRouting(t *testing.T) {
	bridge, err := New(
		[]byte("fake-bridge-conformance-key-0123456789abcdef"),
		func() time.Time { return time.Unix(1785074401, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}
	sink := &adaptersdk.MemoryAssertionSink{}
	if err := bridge.Connect(context.Background(), adaptersdk.BridgeTarget{
		Installation: adaptersdk.Installation{
			InstallationID: "ain_loomwright", AdapterID: AdapterID,
		},
		Protocol: Protocol, SchemaVersion: SchemaVersion,
		Frames: strings.NewReader("pulse|session-seven|event-nine|awake|1785074400"),
	}, sink); err != nil {
		t.Fatal(err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].AdapterID != AdapterID ||
		records[0].EventType != "source.observed" ||
		records[0].Lineage.SessionPseudonym == "session-seven" {
		t.Fatalf("unexpected safe projection: %#v", records)
	}
	if bridge.Health(context.Background()).Lifecycle != adaptersdk.BridgeReconciled {
		t.Fatalf("health=%#v", bridge.Health(context.Background()))
	}
}
