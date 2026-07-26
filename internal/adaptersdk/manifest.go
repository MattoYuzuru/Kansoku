package adaptersdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// ParseManifest decodes a Manifest from raw bytes. Manifests are data,
// never code: this function never evaluates, executes or shells out to
// anything found in raw. It rejects duplicate JSON object keys, unknown
// fields, values exceeding the same maxConfigEntries/maxConfigDepth/
// maxConfigString bounds internal/installer/protocol.go already enforces
// for agent config plans, and it enforces the closed capability/permission
// vocabulary from contracts/adapter-sdk before returning success.
func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) > MaxManifestConfigString*MaxManifestConfigEntries {
		return Manifest{}, errors.New("manifest_too_large")
	}
	if err := rejectDuplicateManifestNames(raw); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, errors.New("invalid_or_unknown_manifest_field")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("trailing_manifest_json")
	}
	if err := validateManifestBounds(raw); err != nil {
		return Manifest{}, err
	}
	if err := validateManifestShape(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifestShape(m Manifest) error {
	if m.APIVersion != AdapterAPIVersion {
		return errors.New("unsupported_manifest_api_version")
	}
	if m.ID == "" || len(m.ID) > MaxManifestConfigString || !validAdapterID(m.ID) {
		return errors.New("invalid_manifest_id")
	}
	if m.Version == "" || len(m.Version) > MaxManifestConfigString {
		return errors.New("invalid_manifest_version")
	}
	switch m.Execution {
	case ExecutionBuiltin, ExecutionExternalProcess, ExecutionWasm, ExecutionContainer:
	default:
		return errors.New("invalid_manifest_execution_form")
	}
	for capability := range m.Capabilities {
		if !validCapabilityID(capability) {
			return errors.New("unknown_manifest_capability_id")
		}
	}
	switch m.Permissions.Network {
	case NetworkNone, NetworkLoopbackOnly:
	default:
		return errors.New("invalid_manifest_network_permission")
	}
	if len(m.Permissions.FilesystemRead) > MaxManifestConfigEntries || len(m.Permissions.ProcessExec) > MaxManifestConfigEntries {
		return errors.New("manifest_permission_list_too_large")
	}
	if len(m.Sources) > MaxManifestConfigEntries {
		return errors.New("manifest_sources_too_large")
	}
	return nil
}

func validAdapterID(id string) bool {
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

func validCapabilityID(id CapabilityID) bool {
	switch id {
	case CapabilityDiscoveryAgentAndSurface, CapabilityInventoryComponents, CapabilityActivitySessions,
		CapabilityActivityPromptMetadata, CapabilityActivityTokenModelCost, CapabilityComponentsSkillInvocation,
		CapabilityComponentsPluginAndCustomCmd, CapabilityComponentsMCPLifecycle, CapabilityComponentsBuiltinToolCalls,
		CapabilityComponentsSubagentsCompaction, CapabilityIngestionHistoricalImport, CapabilityIngestionLiveStream,
		CapabilityIngestionEvidenceBridge, CapabilityConfigurationInstall, CapabilityConfigurationLiveCanary,
		CapabilityConfigurationHookInstall:
		return true
	default:
		return false
	}
}

// validateManifestBounds walks the manifest as generic JSON to enforce the
// same depth/entry/string bounds as internal/installer/protocol.go's
// validateConfig, independent of the concrete Manifest struct shape, so a
// future field addition cannot silently bypass the size ceiling.
func validateManifestBounds(raw []byte) error {
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return errors.New("malformed_manifest_json")
	}
	return boundValue(generic, 1)
}

func boundValue(value any, depth int) error {
	if depth > MaxManifestConfigDepth {
		return errors.New("manifest_depth_exceeded")
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > MaxManifestConfigEntries {
			return errors.New("manifest_entries_exceeded")
		}
		for key, item := range typed {
			if len(key) > MaxManifestConfigString {
				return errors.New("manifest_key_too_long")
			}
			if err := boundValue(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > MaxManifestConfigEntries {
			return errors.New("manifest_entries_exceeded")
		}
		for _, item := range typed {
			if err := boundValue(item, depth+1); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > MaxManifestConfigString {
			return errors.New("manifest_string_too_long")
		}
	case bool, nil, json.Number:
	default:
		return errors.New("manifest_unsupported_type")
	}
	return nil
}

func rejectDuplicateManifestNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	return rejectDuplicateManifestNamesAt(decoder)
}

func rejectDuplicateManifestNamesAt(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("malformed_manifest_json")
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return errors.New("malformed_manifest_json")
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("malformed_manifest_json")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate_manifest_field")
			}
			seen[name] = struct{}{}
			if err := rejectDuplicateManifestNamesAt(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return errors.New("malformed_manifest_json")
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateManifestNamesAt(decoder); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return errors.New("malformed_manifest_json")
		}
	default:
		return errors.New("malformed_manifest_json")
	}
	return nil
}
