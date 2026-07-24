package integrity

import "testing"

func TestChangedAdapterIDsEmptyPreviousNeverReportsChange(t *testing.T) {
	current := AdapterVersionSnapshot{"codex": "1.0.0", "claude": "2.0.0"}
	if got := ChangedAdapterIDs(nil, current); got != nil {
		t.Fatalf("ChangedAdapterIDs(nil, current) = %v, want nil (first observation is not drift)", got)
	}
	if got := ChangedAdapterIDs(AdapterVersionSnapshot{}, current); got != nil {
		t.Fatalf("ChangedAdapterIDs(empty, current) = %v, want nil", got)
	}
}

func TestChangedAdapterIDsDetectsVersionBump(t *testing.T) {
	previous := AdapterVersionSnapshot{"codex": "1.0.0", "claude": "2.0.0"}
	current := AdapterVersionSnapshot{"codex": "1.1.0", "claude": "2.0.0"}
	got := ChangedAdapterIDs(previous, current)
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("ChangedAdapterIDs = %v, want [codex]", got)
	}
}

func TestChangedAdapterIDsDetectsAddedAndRemovedAdapters(t *testing.T) {
	previous := AdapterVersionSnapshot{"codex": "1.0.0"}
	current := AdapterVersionSnapshot{"claude": "2.0.0"}
	got := ChangedAdapterIDs(previous, current)
	if len(got) != 2 {
		t.Fatalf("ChangedAdapterIDs = %v, want both codex (removed) and claude (added)", got)
	}
}

func TestChangedAdapterIDsNoChangeReturnsEmpty(t *testing.T) {
	previous := AdapterVersionSnapshot{"codex": "1.0.0"}
	current := AdapterVersionSnapshot{"codex": "1.0.0"}
	if got := ChangedAdapterIDs(previous, current); len(got) != 0 {
		t.Fatalf("ChangedAdapterIDs = %v, want empty", got)
	}
}

func TestAdvisoryLockKeyIsDeterministic(t *testing.T) {
	a := AdvisoryLockKey()
	b := AdvisoryLockKey()
	if a != b {
		t.Fatalf("AdvisoryLockKey is not deterministic: %d != %d", a, b)
	}
	if a == 0 {
		t.Fatalf("AdvisoryLockKey must not be zero")
	}
}
