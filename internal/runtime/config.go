// Package runtime assembles Kansoku's Session 09 local appliance.
package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ConfigVersion        = "kansoku.runtime-config/1"
	AppVersion           = "0.9.0"
	RuntimeSchemaVersion = "kansoku.runtime-schema/1"
	maxConfigBytes       = 64 << 10
)

// Config is the closed, non-secret appliance configuration. Secret fields
// contain file locators only; secret values are never decoded from config or
// environment.
type Config struct {
	Version                      string            `json:"version"`
	AppVersion                   string            `json:"app_version"`
	HTTPListen                   string            `json:"http_listen"`
	OTLPHTTPListen               string            `json:"otlp_http_listen"`
	OTLPGRPCListen               string            `json:"otlp_grpc_listen"`
	ContainerMode                bool              `json:"container_mode"`
	DataDir                      string            `json:"data_dir"`
	Database                     DBConfig          `json:"database"`
	Secrets                      SecretFiles       `json:"secret_files"`
	QueueCapacity                int               `json:"queue_capacity"`
	SpoolMaxBytes                int64             `json:"spool_max_bytes"`
	CheckpointStateMaxBytes      int64             `json:"checkpoint_state_max_bytes"`
	DatabaseSoftLimitBytes       int64             `json:"database_soft_limit_bytes"`
	DatabaseBudgetWarning        float64           `json:"database_budget_warning_fraction"`
	DatabaseBudgetDegraded       float64           `json:"database_budget_degraded_fraction"`
	DatabaseBudgetCritical       float64           `json:"database_budget_critical_fraction"`
	StoragePreflightMinFreeBytes int64             `json:"storage_preflight_min_free_bytes"`
	ShutdownTimeoutMS            int64             `json:"shutdown_timeout_ms"`
	QueryTimeoutMS               int64             `json:"query_timeout_ms"`
	ResponseMaxBytes             int64             `json:"response_max_bytes"`
	RetentionDays                int               `json:"retention_days"`
	DiskBudgetFraction           float64           `json:"disk_budget_fraction"`
	IntegrityEnabled             bool              `json:"integrity_enabled"`
	PrivacyCanaryFixture         string            `json:"privacy_canary_fixture"`
	BackupDir                    string            `json:"backup_dir"`
	DiagnosticsMaxBytes          int64             `json:"diagnostics_max_bytes"`
	InventoryTargets             []InventoryTarget `json:"inventory_targets,omitempty"`
	InventoryScanIntervalSeconds int               `json:"inventory_scan_interval_seconds,omitempty"`
	RolloutWatchIntervalSeconds  int               `json:"rollout_watch_interval_seconds"`
}

// InventoryTarget is one explicit, read-only adapter state root mounted into
// the appliance. It contains no credential and grants no configuration write.
type InventoryTarget struct {
	TargetID       string `json:"target_id"`
	AdapterID      string `json:"adapter_id"`
	InstallationID string `json:"installation_id,omitempty"`
	SurfaceID      string `json:"surface_id"`
	StateRoot      string `json:"state_root"`
}

type DBConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Name           string `json:"name"`
	User           string `json:"user"`
	SSLMode        string `json:"ssl_mode"`
	ConnectTimeout int    `json:"connect_timeout_seconds"`
}

func LoadConfig(path string) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, errors.New("config_path_must_be_absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("config_open_failed")
	}
	defer file.Close()
	limited := io.LimitReader(file, maxConfigBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil || len(raw) == 0 || len(raw) > maxConfigBytes {
		return Config{}, errors.New("config_read_failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, errors.New("config_schema_invalid")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, errors.New("config_schema_invalid")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing_json")
	}
	return nil
}

func (c Config) Validate() error {
	if c.Version != ConfigVersion || c.AppVersion != AppVersion {
		return errors.New("unsupported_runtime_config_version")
	}
	for label, value := range map[string]string{
		"http": c.HTTPListen, "otlp_http": c.OTLPHTTPListen, "otlp_grpc": c.OTLPGRPCListen,
	} {
		if err := validateListen(value, c.ContainerMode); err != nil {
			return fmt.Errorf("%s_listen_invalid", label)
		}
	}
	if !filepath.IsAbs(c.DataDir) || !filepath.IsAbs(c.BackupDir) ||
		!filepath.IsAbs(c.PrivacyCanaryFixture) ||
		c.DataDir == "/" || c.BackupDir == "/" || c.PrivacyCanaryFixture == "/" ||
		c.DataDir == c.BackupDir {
		return errors.New("runtime_directory_invalid")
	}
	if c.Database.Host == "" || strings.ContainsAny(c.Database.Host, "/@ \t\r\n") ||
		c.Database.Port != 5432 || c.Database.Name != "kansoku" || c.Database.User != "kansoku" ||
		c.Database.SSLMode != "disable" || c.Database.ConnectTimeout < 1 || c.Database.ConnectTimeout > 30 {
		return errors.New("database_config_invalid")
	}
	if c.QueueCapacity != 64 || c.SpoolMaxBytes != 64<<20 ||
		c.CheckpointStateMaxBytes != 4<<20 ||
		c.DatabaseSoftLimitBytes != 5<<30 ||
		c.DatabaseBudgetWarning != 0.70 ||
		c.DatabaseBudgetDegraded != 0.85 ||
		c.DatabaseBudgetCritical != 0.95 ||
		c.StoragePreflightMinFreeBytes != 25<<30 ||
		c.ShutdownTimeoutMS != 30_000 || c.QueryTimeoutMS != 500 ||
		c.ResponseMaxBytes != 1<<20 || c.RetentionDays != 400 ||
		c.DiskBudgetFraction != 0.90 || c.DiagnosticsMaxBytes != 1<<20 {
		return errors.New("runtime_budget_invalid")
	}
	if len(c.InventoryTargets) > 32 {
		return errors.New("inventory_targets_invalid")
	}
	seenTargets := map[string]bool{}
	for _, target := range c.InventoryTargets {
		if !safeInventoryConfigID(target.TargetID) ||
			!safeInventoryConfigID(target.AdapterID) ||
			(target.InstallationID != "" && !safeAgentInstallationID(target.InstallationID)) ||
			!safeInventoryConfigID(target.SurfaceID) ||
			!filepath.IsAbs(target.StateRoot) || target.StateRoot == "/" ||
			seenTargets[target.TargetID] {
			return errors.New("inventory_targets_invalid")
		}
		seenTargets[target.TargetID] = true
	}
	if len(c.InventoryTargets) == 0 {
		if c.InventoryScanIntervalSeconds != 0 {
			return errors.New("inventory_scan_interval_invalid")
		}
	} else if c.InventoryScanIntervalSeconds < 60 || c.InventoryScanIntervalSeconds > 3600 {
		return errors.New("inventory_scan_interval_invalid")
	}
	if c.RolloutWatchIntervalSeconds != 5 {
		return errors.New("rollout_watch_interval_invalid")
	}
	if err := c.Secrets.ValidateLocators(); err != nil {
		return err
	}
	return nil
}

func safeInventoryConfigID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:@/-", char) {
			continue
		}
		return false
	}
	return true
}

func safeAgentInstallationID(value string) bool {
	if len(value) != len("ain_")+32 || !strings.HasPrefix(value, "ain_") {
		return false
	}
	for _, char := range value[len("ain_"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validateListen(value string, containerMode bool) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return errors.New("listen_invalid")
	}
	allowedPort := port == "43100" || port == "4317" || port == "4318"
	if !allowedPort {
		return errors.New("listen_invalid")
	}
	if containerMode {
		if host != "0.0.0.0" {
			return errors.New("container_listen_must_be_wildcard")
		}
		return nil
	}
	if host != "127.0.0.1" && host != "::1" {
		return errors.New("listen_must_be_loopback")
	}
	return nil
}

func (c Config) ShutdownTimeout() time.Duration {
	return time.Duration(c.ShutdownTimeoutMS) * time.Millisecond
}
func (c Config) QueryTimeout() time.Duration {
	return time.Duration(c.QueryTimeoutMS) * time.Millisecond
}

// DatabaseDSN constructs the process-local DSN from non-secret config and
// the separately loaded password. Callers must never log its return value.
func (c Config) DatabaseDSN(password []byte) (string, error) {
	if len(password) < minSecretBytes {
		return "", errors.New("database_password_missing")
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Database.User, string(password)),
		Host:   net.JoinHostPort(c.Database.Host, fmt.Sprintf("%d", c.Database.Port)),
		Path:   c.Database.Name,
	}
	query := u.Query()
	query.Set("sslmode", c.Database.SSLMode)
	query.Set("connect_timeout", fmt.Sprintf("%d", c.Database.ConnectTimeout))
	u.RawQuery = query.Encode()
	return u.String(), nil
}
