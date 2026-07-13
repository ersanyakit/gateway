package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// JSONData keeps structured JSON in PostgreSQL while remaining pleasant to use
// from Go structs. An empty value is stored as SQL NULL.
type JSONData string

func (value JSONData) Value() (driver.Value, error) {
	raw := strings.TrimSpace(string(value))
	if raw == "" {
		return nil, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("invalid JSON data")
	}
	return raw, nil
}

func (value *JSONData) Scan(src any) error {
	if value == nil {
		return fmt.Errorf("JSONData scan target is nil")
	}
	switch raw := src.(type) {
	case nil:
		*value = ""
	case []byte:
		*value = JSONData(string(raw))
	case string:
		*value = JSONData(raw)
	default:
		return fmt.Errorf("unsupported JSONData source %T", src)
	}
	return nil
}

func (value JSONData) MarshalJSON() ([]byte, error) {
	raw := strings.TrimSpace(string(value))
	if raw == "" {
		return []byte("null"), nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("invalid JSON data")
	}
	return []byte(raw), nil
}

func (value *JSONData) UnmarshalJSON(data []byte) error {
	if value == nil {
		return fmt.Errorf("JSONData unmarshal target is nil")
	}
	if string(data) == "null" {
		*value = ""
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON data")
	}
	*value = JSONData(string(data))
	return nil
}
