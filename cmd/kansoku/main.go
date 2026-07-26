package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/observability"
	kansokuruntime "kansoku.local/kansoku/internal/runtime"
)

var bridgeInstallationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/|-]{0,255}$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{
			"timestamp": time.Now().UTC(), "level": "error",
			"event_name": "command_failed", "error_class": kansokuruntime.SafeErrorClass(err),
		})
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("command_required")
	}
	command := arguments[0]
	switch command {
	case "serve", "health", "config", "migrate", "backup", "restore-verify", "export", "import", "diagnostics", "evidence-bridge", "soak":
	default:
		return errors.New("unknown_command")
	}
	// The accelerated soak runs host-side against an already-running appliance
	// and issues real Docker operations; it does not load the strict in-container
	// runtime Config (whose secret locators point at /run/secrets container
	// paths) and takes its own host-side flags instead.
	if command == "soak" {
		return runSoak(arguments[1:])
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "absolute path to the runtime config")
	self := flags.Bool("self", false, "run local appliance self-checks")
	backupID := flags.String("backup-id", "", "opaque backup identifier")
	exportID := flags.String("export-id", "", "opaque export identifier")
	idempotencyKeyFile := flags.String("idempotency-key-file", "", "absolute path to an import idempotency key")
	bridgeID := flags.String("bridge-id", "", "version-pinned evidence bridge identifier")
	installationID := flags.String("installation-id", "", "opaque agent installation identifier")
	flagArgs := arguments[1:]
	if command == "config" && len(flagArgs) > 0 && flagArgs[0] == "check" {
		flagArgs = flagArgs[1:]
	}
	if err := flags.Parse(flagArgs); err != nil {
		return errors.New("invalid_flags")
	}
	if *configPath == "" {
		return errors.New("config_required")
	}
	config, err := kansokuruntime.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	secrets, err := kansokuruntime.LoadSecretFiles(config.Secrets)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch command {
	case "config":
		return writeJSON(map[string]string{"config": "pass", "secret_files": "pass"})
	case "migrate":
		return kansokuruntime.MigrateOnly(ctx, config, secrets)
	case "health":
		if !*self {
			return errors.New("health_self_required")
		}
		result, err := kansokuruntime.ProbeRunningAppliance(ctx, config, secrets)
		if err != nil {
			return err
		}
		return writeJSON(result)
	}
	appliance, err := kansokuruntime.NewAppliance(ctx, config, secrets, nil)
	if err != nil {
		return err
	}
	defer func() { _ = appliance.Shutdown(context.Background()) }()
	switch command {
	case "serve":
		signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return appliance.Run(signalCtx)
	case "backup":
		result, err := appliance.Operations.Backup(ctx, kansokuruntime.BackupRequest{})
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "restore-verify":
		result, err := appliance.Operations.RestoreVerify(ctx, kansokuruntime.RestoreVerifyRequest{BackupID: *backupID})
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "export":
		result, err := appliance.Operations.Export(ctx, kansokuruntime.ExportRequest{})
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "import":
		key, err := readKeyFile(*idempotencyKeyFile)
		if err != nil {
			return err
		}
		result, err := appliance.Operations.Import(ctx, kansokuruntime.ImportRequest{ExportID: *exportID, IdempotencyKey: key})
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "diagnostics":
		result, err := appliance.Operations.Diagnostics(ctx, kansokuruntime.DiagnosticsRequest{})
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "evidence-bridge":
		if *bridgeID != codexadapter.AppServerBridgeID ||
			!bridgeInstallationID.MatchString(*installationID) {
			return errors.New("evidence_bridge_target_invalid")
		}
		bridge, err := codexadapter.NewAppServerBridge(secrets.IdentityHMAC, time.Now)
		if err != nil {
			return err
		}
		sink, err := observability.NewBridgeAssertionSink(appliance.Ingestor)
		if err != nil {
			return err
		}
		if err := bridge.Connect(ctx, adaptersdk.BridgeTarget{
			Installation: adaptersdk.Installation{
				InstallationID: *installationID,
				AdapterID:      codexadapter.AdapterID,
			},
			Protocol:      codexadapter.AppServerProtocolVersion,
			SchemaVersion: codexadapter.AppServerSchemaVersion,
			Frames:        os.Stdin,
		}, sink); err != nil {
			return err
		}
		return writeJSON(bridge.Health(ctx))
	default:
		return errors.New("unknown_command")
	}
}

func readKeyFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("idempotency_key_file_required")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 16 || len(raw) > 256 {
		return "", errors.New("idempotency_key_file_invalid")
	}
	if raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	return string(raw), nil
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(true)
	return encoder.Encode(value)
}
