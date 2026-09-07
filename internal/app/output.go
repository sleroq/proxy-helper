package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config redaction is defense in depth, not a public export format. The source
// config remains permission protected; arbitrary protocol extensions may add
// secret fields not recognized here.
func (m Manager) Config(raw bool) ([]byte, error) {
	if raw && os.Geteuid() != 0 {
		return nil, fmt.Errorf("sb config --raw requires root")
	}
	data, err := os.ReadFile(m.Settings.ConfigPath())
	if err != nil {
		return nil, err
	}
	if raw {
		return data, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	redact(value)
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func redact(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			switch strings.ToLower(key) {
			case "password", "uuid", "private_key", "token", "auth_str", "secret", "authorization", "username", "headers":
				v[key] = "<redacted>"
			default:
				redact(item)
			}
		}
	case []any:
		for _, item := range v {
			redact(item)
		}
	}
}
