package managerstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxInputSchemaBytes = 64 << 10
const MaxWorkflowInputBytes = 1 << 20

func number(value any) (*big.Rat, bool) {
	n, ok := value.(json.Number)
	if !ok || len(n) > 4096 {
		return nil, false
	}
	if i := strings.IndexAny(string(n), "eE"); i >= 0 {
		exponent, err := strconv.Atoi(string(n)[i+1:])
		if err != nil || exponent > 4096 || exponent < -4096 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(string(n))
}
func validateJSONNumbers(value any) error {
	switch v := value.(type) {
	case json.Number:
		if _, ok := number(v); !ok {
			return errors.New("JSON number exceeds supported numeric domain")
		}
	case map[string]any:
		for _, child := range v {
			if err := validateJSONNumbers(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := validateJSONNumbers(child); err != nil {
				return err
			}
		}
	}
	return nil
}
func schemaJSON(data []byte) (any, error) {
	var value, extra any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("expected one JSON value")
	}
	if err := validateJSONNumbers(value); err != nil {
		return nil, err
	}
	return value, nil
}
func ValidateInputSchema(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > MaxInputSchemaBytes {
		return errors.New("input_schema exceeds 64 KiB")
	}
	value, err := schemaJSON(data)
	if err != nil {
		return err
	}
	return checkSchema(value, 0)
}
func checkSchema(value any, depth int) error {
	schema, ok := value.(map[string]any)
	if !ok || depth > 32 {
		return errors.New("schema must be an object at depth at most 32")
	}
	for key, value := range schema {
		switch key {
		case "title", "description":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "type":
			switch value {
			case "object", "array", "string", "number", "integer", "boolean", "null":
			default:
				return errors.New("unsupported schema type")
			}
		case "properties":
			props, ok := value.(map[string]any)
			if !ok {
				return errors.New("properties must be an object")
			}
			for _, child := range props {
				if err := checkSchema(child, depth+1); err != nil {
					return err
				}
			}
		case "items":
			if err := checkSchema(value, depth+1); err != nil {
				return err
			}
		case "additionalProperties":
			if _, ok := value.(bool); !ok {
				return errors.New("additionalProperties must be boolean")
			}
		case "required":
			list, ok := value.([]any)
			if !ok {
				return errors.New("required must be an array")
			}
			seen := map[string]bool{}
			for _, item := range list {
				name, ok := item.(string)
				if !ok || seen[name] {
					return errors.New("required must contain unique strings")
				}
				seen[name] = true
			}
		case "enum":
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				return errors.New("enum must be nonempty")
			}
			for i, item := range list {
				for _, previous := range list[:i] {
					if sameJSON(item, previous) {
						return errors.New("enum values must be unique")
					}
				}
			}
		case "const":
		case "minimum", "maximum":
			if _, ok := number(value); !ok {
				return errors.New("numeric bound required")
			}
		case "minLength", "maxLength", "minItems", "maxItems":
			n, ok := number(value)
			if !ok || !n.IsInt() || n.Sign() < 0 || !n.Num().IsInt64() {
				return errors.New("length bound must be nonnegative int64")
			}
		default:
			return fmt.Errorf("unsupported schema keyword %s", key)
		}
	}
	return nil
}
func sameJSON(a, b any) bool {
	if n, ok := number(a); ok {
		m, ok := number(b)
		return ok && n.Cmp(m) == 0
	}
	switch v := a.(type) {
	case nil:
		return b == nil
	case bool:
		w, ok := b.(bool)
		return ok && v == w
	case string:
		w, ok := b.(string)
		return ok && v == w
	case []any:
		w, ok := b.([]any)
		if !ok || len(v) != len(w) {
			return false
		}
		for i, x := range v {
			if !sameJSON(x, w[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		w, ok := b.(map[string]any)
		if !ok || len(v) != len(w) {
			return false
		}
		for k, x := range v {
			y, ok := w[k]
			if !ok || !sameJSON(x, y) {
				return false
			}
		}
		return true
	}
	return false
}
func ValidateWorkflowInputs(schema, inputs json.RawMessage) error {
	if err := ValidateInputSchema(schema); err != nil {
		return err
	}
	if len(inputs) > MaxWorkflowInputBytes {
		return errors.New("workflow inputs exceed 1 MiB")
	}
	value, err := schemaJSON(inputs)
	if err != nil {
		return err
	}
	if len(schema) == 0 {
		return nil
	}
	document, _ := schemaJSON(schema)
	return matchSchema(document.(map[string]any), value)
}
func matchSchema(schema map[string]any, value any) error {
	actual := "null"
	switch value.(type) {
	case map[string]any:
		actual = "object"
	case []any:
		actual = "array"
	case string:
		actual = "string"
	case bool:
		actual = "boolean"
	case json.Number:
		actual = "number"
	}
	if wanted, ok := schema["type"].(string); ok && wanted != actual {
		n, isNumber := number(value)
		if wanted != "integer" || !isNumber || !n.IsInt() {
			return errors.New("input type mismatch")
		}
	}
	if expected, ok := schema["const"]; ok && !sameJSON(expected, value) {
		return errors.New("input differs from const")
	}
	if choices, ok := schema["enum"].([]any); ok {
		found := false
		for _, choice := range choices {
			if sameJSON(choice, value) {
				found = true
				break
			}
		}
		if !found {
			return errors.New("input outside enum")
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, ok := v[key.(string)]; !ok {
					return fmt.Errorf("missing required property %s", key)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for key, child := range v {
			if def, ok := props[key]; ok {
				if err := matchSchema(def.(map[string]any), child); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			} else if allowed, ok := schema["additionalProperties"].(bool); ok && !allowed {
				return fmt.Errorf("unexpected property %s", key)
			}
		}
	case []any:
		if err := matchLength(schema, "Items", len(v)); err != nil {
			return err
		}
		if item, ok := schema["items"].(map[string]any); ok {
			for _, child := range v {
				if err := matchSchema(item, child); err != nil {
					return err
				}
			}
		}
	case string:
		return matchLength(schema, "Length", utf8.RuneCountInString(v))
	case json.Number:
		n, _ := number(v)
		if bound, ok := number(schema["minimum"]); ok && n.Cmp(bound) < 0 {
			return errors.New("input below minimum")
		}
		if bound, ok := number(schema["maximum"]); ok && n.Cmp(bound) > 0 {
			return errors.New("input above maximum")
		}
	}
	return nil
}
func matchLength(schema map[string]any, suffix string, length int) error {
	n := new(big.Rat).SetInt64(int64(length))
	if bound, ok := number(schema["min"+suffix]); ok && n.Cmp(bound) < 0 {
		return errors.New("input shorter than minimum")
	}
	if bound, ok := number(schema["max"+suffix]); ok && n.Cmp(bound) > 0 {
		return errors.New("input longer than maximum")
	}
	return nil
}
