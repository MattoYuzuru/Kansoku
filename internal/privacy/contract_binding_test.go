package privacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type ingressContract struct {
	Limits struct {
		MaxTotalBytes    int64 `json:"max_total_bytes"`
		MaxDepth         int   `json:"max_depth"`
		MaxArrayItems    int   `json:"max_array_items"`
		MaxObjectFields  int   `json:"max_object_fields"`
		MaxStringBytes   int   `json:"max_string_bytes"`
		MaxNumberBytes   int   `json:"max_number_bytes"`
		MaxRecords       int   `json:"max_records"`
		MaxProtobufFrame int64 `json:"max_protobuf_frame_bytes"`
	} `json:"limits"`
	SourceSchemas []struct {
		ID             string   `json:"id"`
		AdapterID      string   `json:"adapter_id"`
		AdapterVersion string   `json:"adapter_version"`
		EventTypes     []string `json:"event_types"`
		Models         []string `json:"models"`
		Tools          []string `json:"tools"`
		Components     []string `json:"components"`
		InputFields    []string `json:"input_fields"`
	} `json:"source_schemas"`
	DurableRecordFields  []string `json:"durable_record_fields"`
	SafeErrorFields      []string `json:"safe_error_fields"`
	PrivacySafeLogFields []string `json:"privacy_safe_log_fields"`
	NestedTypes          map[string]struct {
		Fields map[string]string `json:"fields"`
	} `json:"nested_types"`
	Enums struct {
		ValueStates []string `json:"value_states"`
		Outcomes    []string `json:"outcomes"`
	} `json:"enums"`
}

func TestRuntimeBoundaryMatchesAuthoritativeIngressContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "privacy", "ingress.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract ingressContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	if limits.MaxTotalBytes != contract.Limits.MaxTotalBytes || limits.MaxDepth != contract.Limits.MaxDepth || limits.MaxArrayItems != contract.Limits.MaxArrayItems || limits.MaxObjectFields != contract.Limits.MaxObjectFields || limits.MaxStringBytes != contract.Limits.MaxStringBytes || limits.MaxNumberBytes != contract.Limits.MaxNumberBytes || limits.MaxRecords != contract.Limits.MaxRecords || limits.MaxProtobufFrame != contract.Limits.MaxProtobufFrame {
		t.Fatal("runtime limits drifted from ingress registry")
	}
	if len(contract.SourceSchemas) != 1 {
		t.Fatal("unexpected source schema population")
	}
	expected := contract.SourceSchemas[0]
	actual := FixtureSourceSchema()
	if actual.ID != expected.ID || actual.AdapterID != expected.AdapterID || actual.AdapterVersion != expected.AdapterVersion || !reflect.DeepEqual(actual.EventTypes, sliceSet(expected.EventTypes)) || !reflect.DeepEqual(actual.Models, sliceSet(expected.Models)) || !reflect.DeepEqual(actual.Tools, sliceSet(expected.Tools)) || !reflect.DeepEqual(actual.Components, sliceSet(expected.Components)) || !reflect.DeepEqual(actual.InputFields, sliceSet(expected.InputFields)) {
		t.Fatal("runtime fixture schema/catalog drifted from ingress registry")
	}
	if !reflect.DeepEqual(valueStates, sliceSet(contract.Enums.ValueStates)) || !reflect.DeepEqual(outcomes, sliceSet(contract.Enums.Outcomes)) {
		t.Fatal("runtime enums drifted from ingress registry")
	}
	bindings := map[string]struct {
		typ    reflect.Type
		fields []string
	}{
		"SafeRecord": {reflect.TypeOf(SafeRecord{}), contract.DurableRecordFields}, "SafeError": {reflect.TypeOf(SafeError{}), contract.SafeErrorFields}, "SafeLogEvent": {reflect.TypeOf(SafeLogEvent{}), contract.PrivacySafeLogFields},
		"CatalogObservation": {reflect.TypeOf(CatalogObservation{}), mapKeys(contract.NestedTypes["CatalogObservation"].Fields)}, "PromptFeatures": {reflect.TypeOf(PromptFeatures{}), mapKeys(contract.NestedTypes["PromptFeatures"].Fields)}, "RedactionCounts": {reflect.TypeOf(RedactionCounts{}), mapKeys(contract.NestedTypes["RedactionCounts"].Fields)}, "Lineage": {reflect.TypeOf(Lineage{}), mapKeys(contract.NestedTypes["Lineage"].Fields)},
	}
	for name, binding := range bindings {
		if !reflect.DeepEqual(jsonFields(binding.typ), stringSliceSet(binding.fields)) {
			t.Fatalf("%s JSON fields drifted", name)
		}
	}
}

func TestPrivacyRegistryAggregateHashMatchesRuntimeConstant(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "contracts", "privacy", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		canonical, _ := json.Marshal(value)
		relative := filepath.ToSlash(filepath.Join("contracts", "privacy", filepath.Base(path)))
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		hash.Write(canonical)
		hash.Write([]byte{0})
	}
	if hex.EncodeToString(hash.Sum(nil)) != PrivacyContractSemanticSHA256 {
		t.Fatal("privacy registry aggregate hash drift")
	}
}

func sliceSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func stringSliceSet(values []string) map[string]struct{} { return sliceSet(values) }
func mapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}
func jsonFields(value reflect.Type) map[string]struct{} {
	result := map[string]struct{}{}
	for index := 0; index < value.NumField(); index++ {
		result[value.Field(index).Tag.Get("json")] = struct{}{}
	}
	return result
}
