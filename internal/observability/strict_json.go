package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// strictUnmarshal rejects duplicate names before encoding/json can collapse
// them, rejects unknown struct fields, and requires exactly one JSON value.
func strictUnmarshal(raw []byte, destination any) error {
	structure := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDuplicateNames(structure); err != nil {
		return err
	}
	if _, err := structure.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid_or_unknown_json_field")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing_json")
	}
	return nil
}

func rejectDuplicateNames(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("malformed_json")
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
				return errors.New("malformed_json")
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("malformed_json")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate_json_field")
			}
			seen[name] = struct{}{}
			if err := rejectDuplicateNames(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed_json")
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateNames(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed_json")
		}
	default:
		return errors.New("malformed_json")
	}
	return nil
}
