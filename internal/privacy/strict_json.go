package privacy

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
)

// decodeStrictJSON rejects ambiguities before a map can collapse them. The
// total-byte limit is applied before this function, so materializing the
// structural tree is bounded. Semantic bounds are evaluated afterwards in a
// deterministic sorted traversal.
func decodeStrictJSON(raw []byte, limits Limits) (any, string) {
	if !validEscapedUnicode(raw) {
		return nil, "invalid_unicode_scalar"
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, category := decodeStrictValue(decoder, limits.MaxNumberBytes)
	if category != "" {
		return nil, category
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, "trailing_json"
	}
	return value, ""
}

func decodeStrictValue(decoder *json.Decoder, maxNumberBytes int) (any, string) {
	token, err := decoder.Token()
	if err != nil {
		return nil, "malformed_json"
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return nil, "malformed_json"
				}
				name, ok := nameToken.(string)
				if !ok {
					return nil, "malformed_json"
				}
				if _, duplicate := object[name]; duplicate {
					return nil, "duplicate_field"
				}
				value, category := decodeStrictValue(decoder, maxNumberBytes)
				if category != "" {
					return nil, category
				}
				object[name] = value
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, "malformed_json"
			}
			return object, ""
		case '[':
			array := []any{}
			for decoder.More() {
				value, category := decodeStrictValue(decoder, maxNumberBytes)
				if category != "" {
					return nil, category
				}
				array = append(array, value)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, "malformed_json"
			}
			return array, ""
		default:
			return nil, "malformed_json"
		}
	case json.Number:
		if maxNumberBytes <= 0 || len(typed.String()) > maxNumberBytes {
			return nil, "numeric_range"
		}
		number, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, "numeric_range"
		}
		return typed, ""
	case string, bool, nil:
		return typed, ""
	default:
		return nil, "malformed_json"
	}
}

func validEscapedUnicode(raw []byte) bool {
	inString := false
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(raw) {
				continue
			}
			index++
			if raw[index] != 'u' {
				continue
			}
			value, ok := hexQuad(raw, index+1)
			if !ok {
				continue // encoding/json classifies malformed escapes.
			}
			index += 4
			if value >= 0xD800 && value <= 0xDBFF {
				if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
					return false
				}
				low, ok := hexQuad(raw, index+3)
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				index += 6
			} else if value >= 0xDC00 && value <= 0xDFFF {
				return false
			}
		}
	}
	return true
}

func hexQuad(raw []byte, start int) (uint16, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for _, current := range raw[start : start+4] {
		value <<= 4
		switch {
		case current >= '0' && current <= '9':
			value += uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value += uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			value += uint16(current-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
