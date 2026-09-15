package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The envelope contract lists the durable field names of the event envelope,
// and until this test existed nothing compared that list to the structs that
// actually get marshalled. The list drifted: component_evidence and activity
// were durable event fields the contract never named, scope_fields was missing
// message_id and user_id, and measurement_fields was five measurements behind.
// Silent drift in an allowlist contract is the one failure this repository
// cannot tolerate, because the allowlist is the privacy boundary's own record
// of what may be persisted.
//
// The test compares JSON tags, not Go field names: the tag is what reaches the
// durable record, so the tag is what the contract governs.
func TestEnvelopeContractNamesEveryDurableEnvelopeField(t *testing.T) {
	contract := loadEnvelopeContract(t)
	for _, testCase := range []struct {
		contractKey string
		value       any
	}{
		{"event_fields", Event{}},
		{"source_fields", SourceRef{}},
		{"scope_fields", Scope{}},
		{"subject_fields", Subject{}},
		{"component_evidence_fields", ComponentEvidenceMetadata{}},
		{"activity_fields", ActivityMetadata{}},
		{"measurement_fields", Measurements{}},
		{"evidence_fields", Evidence{}},
		{"evidence_assertion_fields", EvidenceAssertion{}},
	} {
		declared := contractStrings(t, contract, testCase.contractKey)
		actual := jsonFieldNames(reflect.TypeOf(testCase.value))
		if !reflect.DeepEqual(declared, actual) {
			t.Errorf("%s: contract declares %v, struct marshals %v",
				testCase.contractKey, declared, actual)
		}
	}
}

// A forbidden field must never become a durable envelope field under any
// name, in any nested struct. This is the direction that matters: the list
// above proves the contract is complete, this proves it is not permissive.
func TestEnvelopeForbiddenFieldsAreAbsentFromEveryDurableStruct(t *testing.T) {
	contract := loadEnvelopeContract(t)
	forbidden := make(map[string]struct{})
	for _, name := range contractStrings(t, contract, "forbidden_fields") {
		forbidden[name] = struct{}{}
	}
	for _, value := range []any{
		Event{}, SourceRef{}, Scope{}, Subject{}, ComponentEvidenceMetadata{},
		ActivityMetadata{}, Measurements{}, Evidence{}, EvidenceAssertion{},
	} {
		structType := reflect.TypeOf(value)
		for _, name := range jsonFieldNames(structType) {
			if _, bad := forbidden[name]; bad {
				t.Errorf("%s declares forbidden durable field %q", structType.Name(), name)
			}
		}
	}
}

func loadEnvelopeContract(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "observability", "envelope.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envelope contract: %v", err)
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decode envelope contract: %v", err)
	}
	return contract
}

func contractStrings(t *testing.T, contract map[string]any, key string) []string {
	t.Helper()
	entries, ok := contract[key].([]any)
	if !ok {
		t.Fatalf("envelope contract key %q is not a list", key)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok {
			t.Fatalf("envelope contract key %q holds a non-string entry", key)
		}
		names = append(names, name)
	}
	return names
}

// jsonFieldNames returns the marshalled field names of a struct in declaration
// order. A field tagged "-" is not durable and is skipped; an untagged
// exported field is reported under its Go name, which will not match the
// contract and is therefore surfaced rather than silently accepted.
func jsonFieldNames(structType reflect.Type) []string {
	names := make([]string, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			tagName, _, _ := cutString(tag, ",")
			if tagName == "-" {
				continue
			}
			if tagName != "" {
				name = tagName
			}
		}
		names = append(names, name)
	}
	return names
}

func cutString(value, separator string) (before, after string, found bool) {
	for index := 0; index+len(separator) <= len(value); index++ {
		if value[index:index+len(separator)] == separator {
			return value[:index], value[index+len(separator):], true
		}
	}
	return value, "", false
}
