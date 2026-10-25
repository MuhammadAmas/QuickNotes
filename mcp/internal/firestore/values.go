// Package firestore is a small client for the Firestore REST API (v1): typed-value encoding,
// structured queries, single-document reads, creates and atomic commits. It knows nothing
// about notes or MCP; callers pass plain Go values (string, bool, int, float64, nil,
// []any, map[string]any) and get the same kinds back.
package firestore

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Encode turns a plain Go value into a Firestore REST value ({"stringValue": "..."} and so on).
// Whole float64 numbers are stored as integers, like the apps' millisecond "order" values.
func Encode(v any) (map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return map[string]any{"nullValue": nil}, nil
	case string:
		return map[string]any{"stringValue": x}, nil
	case bool:
		return map[string]any{"booleanValue": x}, nil
	case int:
		return map[string]any{"integerValue": strconv.Itoa(x)}, nil
	case int64:
		return map[string]any{"integerValue": strconv.FormatInt(x, 10)}, nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return map[string]any{"integerValue": strconv.FormatInt(int64(x), 10)}, nil
		}
		return map[string]any{"doubleValue": x}, nil
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return Encode(items)
	case []any:
		values := make([]any, 0, len(x))
		for _, item := range x {
			ev, err := Encode(item)
			if err != nil {
				return nil, err
			}
			values = append(values, ev)
		}
		if len(values) == 0 {
			return map[string]any{"arrayValue": map[string]any{}}, nil
		}
		return map[string]any{"arrayValue": map[string]any{"values": values}}, nil
	case map[string]any:
		fields, err := EncodeFields(x)
		if err != nil {
			return nil, err
		}
		return map[string]any{"mapValue": map[string]any{"fields": fields}}, nil
	default:
		return nil, fmt.Errorf("firestore: cannot encode %T", v)
	}
}

// EncodeFields encodes every entry of a document's fields.
func EncodeFields(m map[string]any) (map[string]any, error) {
	fields := make(map[string]any, len(m))
	for k, v := range m {
		ev, err := Encode(v)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", k, err)
		}
		fields[k] = ev
	}
	return fields, nil
}

// Decode turns a Firestore REST value back into a plain Go value. Integers come back as int64,
// doubles as float64, timestamps as their RFC 3339 string.
func Decode(raw json.RawMessage) (any, error) {
	var v map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	for kind, inner := range v {
		switch kind {
		case "nullValue":
			return nil, nil
		case "stringValue", "timestampValue", "referenceValue":
			var s string
			err := json.Unmarshal(inner, &s)
			return s, err
		case "booleanValue":
			var b bool
			err := json.Unmarshal(inner, &b)
			return b, err
		case "integerValue":
			var s string
			if err := json.Unmarshal(inner, &s); err != nil {
				return nil, err
			}
			return strconv.ParseInt(s, 10, 64)
		case "doubleValue":
			var f float64
			err := json.Unmarshal(inner, &f)
			return f, err
		case "arrayValue":
			var a struct {
				Values []json.RawMessage `json:"values"`
			}
			if err := json.Unmarshal(inner, &a); err != nil {
				return nil, err
			}
			out := make([]any, 0, len(a.Values))
			for _, item := range a.Values {
				dv, err := Decode(item)
				if err != nil {
					return nil, err
				}
				out = append(out, dv)
			}
			return out, nil
		case "mapValue":
			var m struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			if err := json.Unmarshal(inner, &m); err != nil {
				return nil, err
			}
			return DecodeFields(m.Fields)
		}
	}
	return nil, nil // unknown kinds (bytes, geo points) are not used by QuickNotes
}

// DecodeFields decodes a document's fields.
func DecodeFields(fields map[string]json.RawMessage) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	for k, raw := range fields {
		v, err := Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// SortedKeys returns m's keys in order (stable update masks make requests reproducible).
func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
