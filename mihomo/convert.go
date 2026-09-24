package mihomo

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sleroq/sb/proxy"
)

func convert(node proxy.Node) (map[string]json.RawMessage, error) {
	tag := node.String("tag")
	kind := node.String("type")
	fields, err := protocolFields(kind)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", tag, err)
	}

	out, err := convertFields(node, fields, kind, tag)
	if err != nil {
		return nil, err
	}

	if raw, ok := node["tls"]; ok {
		if err := convertTLS(raw, out); err != nil {
			return nil, fmt.Errorf("node %q: %w", tag, err)
		}
	}
	if raw, ok := node["transport"]; ok {
		if err := convertTransport(raw, out); err != nil {
			return nil, fmt.Errorf("node %q: %w", tag, err)
		}
	}
	if err := checkNodeFields(node, fields, tag); err != nil {
		return nil, err
	}
	return out, nil
}

func convertFields(node proxy.Node, fields map[string]string, kind, tag string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for key, value := range node {
		if target, ok := fields[key]; ok {
			out[target] = value
		}
	}
	if kind == "shadowsocks" || kind == "ss" {
		out["type"] = json.RawMessage(`"ss"`)
	}
	if err := validateRequiredFields(out, kind, tag); err != nil {
		return nil, err
	}

	return out, nil
}

func validateRequiredFields(out map[string]json.RawMessage, kind, tag string) error {
	if _, ok := out["name"]; !ok || tag == "" {
		return fmt.Errorf("node %q: missing tag", tag)
	}
	for _, key := range []string{"server", "port"} {
		if len(out[key]) == 0 {
			return fmt.Errorf("node %q: missing %s", tag, key)
		}
	}
	required := "password"
	if kind == "vmess" || kind == "vless" {
		required = "uuid"
	}
	if len(out[required]) == 0 {
		return fmt.Errorf("node %q: missing %s", tag, required)
	}
	return nil
}

func checkNodeFields(node proxy.Node, fields map[string]string, tag string) error {
	for key := range node {
		if _, ok := fields[key]; !ok && key != "tls" && key != "transport" {
			return fmt.Errorf("node %q: unsupported field %s", tag, key)
		}
	}
	if strings.TrimSpace(tag) == "" {
		return fmt.Errorf("node: empty tag")
	}
	return nil
}

func protocolFields(kind string) (map[string]string, error) {
	fields := map[string]string{"tag": "name", "type": "type", "server": "server", "server_port": "port"}
	switch kind {
	case "shadowsocks", "ss":
		fields["method"] = "cipher"
		fields["password"] = "password"
	case "trojan":
		fields["password"] = "password"
	case "vmess":
		fields["uuid"] = "uuid"
		fields["security"] = "cipher"
		fields["alter_id"] = "alterId"
	case "vless":
		fields["uuid"] = "uuid"
		fields["flow"] = "flow"
	default:
		return nil, fmt.Errorf("unsupported protocol")
	}
	return fields, nil
}

func convertTLS(raw json.RawMessage, out map[string]json.RawMessage) error {
	var tls map[string]json.RawMessage
	if json.Unmarshal(raw, &tls) != nil || tls == nil {
		return fmt.Errorf("invalid TLS")
	}

	return mapTLSOptions(tls, out)
}

func mapTLSOptions(tls map[string]json.RawMessage, out map[string]json.RawMessage) error {
	simple := map[string]string{
		"enabled": "tls", "server_name": "servername",
		"insecure": "skip-cert-verify", "alpn": "alpn",
	}
	for key, value := range tls {
		if target, ok := simple[key]; ok {
			out[target] = value
			continue
		}
		switch key {
		case "utls":
			if err := convertUTLS(value, out); err != nil {
				return err
			}
		case "reality":
			if err := convertReality(value, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported TLS option %s", key)
		}
	}
	return nil
}

func convertUTLS(raw json.RawMessage, out map[string]json.RawMessage) error {
	var utls map[string]json.RawMessage
	if json.Unmarshal(raw, &utls) != nil || utls == nil {
		return fmt.Errorf("invalid uTLS")
	}
	for key, value := range utls {
		switch key {
		case "enabled":
		case "fingerprint":
			out["client-fingerprint"] = value
		default:
			return fmt.Errorf("unsupported uTLS option %s", key)
		}
	}
	return nil
}

func convertReality(raw json.RawMessage, out map[string]json.RawMessage) error {
	var reality map[string]json.RawMessage
	if json.Unmarshal(raw, &reality) != nil || reality == nil {
		return fmt.Errorf("invalid Reality")
	}
	mapped := map[string]json.RawMessage{}
	for key, value := range reality {
		switch key {
		case "enabled":
		case "public_key":
			mapped["public-key"] = value
		case "short_id":
			mapped["short-id"] = value
		default:
			return fmt.Errorf("unsupported Reality option %s", key)
		}
	}
	encoded, _ := json.Marshal(mapped)
	out["reality-opts"] = encoded
	if _, ok := out["client-fingerprint"]; !ok {
		out["client-fingerprint"] = json.RawMessage(`"chrome"`)
	}
	return nil
}

func convertTransport(raw json.RawMessage, out map[string]json.RawMessage) error {
	var transport map[string]json.RawMessage
	if json.Unmarshal(raw, &transport) != nil || transport == nil {
		return fmt.Errorf("invalid transport")
	}
	var typ string
	if json.Unmarshal(transport["type"], &typ) != nil {
		return fmt.Errorf("invalid transport type")
	}

	switch typ {
	case "tcp":
		for key := range transport {
			if key != "type" {
				return fmt.Errorf("unsupported tcp option %s", key)
			}
		}
	case "ws", "grpc":
		out["network"] = transport["type"]
		opts, err := transportOptions(typ, transport)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(opts)
		out[typ+"-opts"] = encoded
	default:
		return fmt.Errorf("unsupported transport %s", typ)
	}
	return nil
}

func transportOptions(typ string, transport map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	opts := map[string]json.RawMessage{}
	for key, value := range transport {
		if key == "type" {
			continue
		}
		if typ == "ws" {
			if key != "path" && key != "headers" {
				return nil, fmt.Errorf("unsupported ws option %s", key)
			}
			opts[key] = value
			continue
		}
		if key != "service_name" {
			return nil, fmt.Errorf("unsupported grpc option %s", key)
		}
		opts["grpc-service-name"] = value
	}
	return opts, nil
}
