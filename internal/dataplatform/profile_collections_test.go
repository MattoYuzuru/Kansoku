package dataplatform

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptyProfileCollectionsMarshalAsArrays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response any
		fields   []string
	}{
		{
			name:     "skill",
			response: newSkillProfileResponse(),
			fields:   []string{"assertions", "sources", "file_tree"},
		},
		{
			name:     "plugin",
			response: newPluginProfileResponse(),
			fields:   []string{"children", "versions", "assertions", "sources"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.response)
			if err != nil {
				t.Fatalf("marshal profile: %v", err)
			}
			for _, field := range tt.fields {
				if strings.Contains(string(raw), `"`+field+`":null`) {
					t.Errorf("%s collection serialized as null: %s", field, raw)
				}
				if !strings.Contains(string(raw), `"`+field+`":[]`) {
					t.Errorf("%s collection did not serialize as an empty array: %s", field, raw)
				}
			}
		})
	}
}
