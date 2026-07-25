package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRuntimeConfigFixtureLoadsAndValidates proves
// tests/fixtures/session-09/runtime-config.json is not a stale, hand-typed
// placeholder: it is the exact bytes of deploy/runtime-config.json (the
// config already proven to run the real container), and it must load and
// validate through the production LoadConfig/Config.Validate path unchanged.
func TestRuntimeConfigFixtureLoadsAndValidates(t *testing.T) {
	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "tests", "fixtures", "session-09", "runtime-config.json"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	config, err := LoadConfig(fixturePath)
	if err != nil {
		t.Fatalf("LoadConfig(%s) error = %v, want nil", fixturePath, err)
	}
	if config.Version != ConfigVersion || config.AppVersion != AppVersion {
		t.Fatalf("config version mismatch: %+v", config)
	}
	if !config.ContainerMode || config.HTTPListen != "0.0.0.0:43100" {
		t.Fatalf("fixture must exercise the container_mode=true wildcard-listener branch: %+v", config)
	}
	if err := config.Secrets.ValidateLocators(); err != nil {
		t.Fatalf("Secrets.ValidateLocators() error = %v, want nil", err)
	}

	fixtureRaw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", fixturePath, err)
	}
	deployPath, err := filepath.Abs(filepath.Join("..", "..", "deploy", "runtime-config.json"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	deployRaw, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", deployPath, err)
	}
	if string(fixtureRaw) != string(deployRaw) {
		t.Fatal("tests/fixtures/session-09/runtime-config.json has drifted from deploy/runtime-config.json; they must stay byte-identical so the fixture keeps proving the production config actually loads")
	}
}
