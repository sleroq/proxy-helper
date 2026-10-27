package mihomo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestConvertHysteria2(t *testing.T) {
	for _, tc := range []struct {
		name, options, want string
	}{
		{
			name: "TLS and salamander",
			options: `"password":"secret",
				"tls":{"enabled":true,"server_name":"tls.example","alpn":["h3","custom"],"insecure":true},
				"obfs":{"type":"salamander","password":"obfs-secret"}`,
			want: `"password":"secret","sni":"tls.example","alpn":["h3","custom"],
				"skip-cert-verify":true,"obfs":"salamander","obfs-password":"obfs-secret"`,
		},
		{
			name:    "minimal TLS without obfs",
			options: `"password":"secret","tls":{"enabled":true}`,
			want:    `"password":"secret"`,
		},
		{
			name:    "optional authentication and explicit TLS defaults",
			options: `"password":"","tls":{"enabled":true,"alpn":[],"insecure":false}`,
			want:    `"password":"","alpn":[],"skip-cert-verify":false`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(node(`{"tag":"leaf","type":"hysteria2","server":"host","server_port":443,` + tc.options + `}`))
			if err != nil {
				t.Fatal(err)
			}
			want := node(`{"name":"leaf","type":"hysteria2","server":"host","port":443,` + tc.want + `}`)
			if !reflect.DeepEqual(got, map[string]json.RawMessage(want)) {
				t.Fatalf("got %v; want %v", got, want)
			}
		})
	}
}

func TestConvertHysteria2RejectsUnsupportedOptions(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, want string
	}{
		{"top-level field", "up_mbps", `100`, "unsupported field up_mbps"},
		{"transport", "transport", `{"type":"ws","path":"/"}`, "unsupported field transport"},
		{"TLS option", "tls", `{"enabled":true,"certificate":"private-value"}`, "unsupported TLS option certificate"},
		{"uTLS", "tls", `{"enabled":true,"utls":{"enabled":true,"fingerprint":"chrome"}}`, "unsupported TLS option utls"},
		{"Reality", "tls", `{"enabled":true,"reality":{"enabled":true,"public_key":"private-value"}}`, "unsupported TLS option reality"},
		{"disabled TLS", "tls", `{"enabled":false}`, "requires enabled TLS"},
		{"invalid TLS", "tls", `null`, "invalid TLS"},
		{"obfs type", "obfs", `{"type":"gecko","password":"private-value"}`, "unsupported obfs type"},
		{"obfs option", "obfs", `{"type":"salamander","password":"private-value","extra":"private-value"}`, "unsupported obfs option extra"},
		{"invalid obfs", "obfs", `null`, "invalid obfs"},
		{"missing obfs password", "obfs", `{"type":"salamander"}`, "invalid obfs password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := node(`{
				"tag":"leaf", "type":"hysteria2", "server":"host", "server_port":443,
				"password":"secret", "tls":{"enabled":true}
			}`)
			n[tc.field] = json.RawMessage(tc.value)
			_, err := convert(n)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want error containing %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatalf("error leaked option value: %v", err)
			}
		})
	}
}

func TestConvertTrojanTLSKeepsServername(t *testing.T) {
	got, err := convert(node(`{
		"tag":"leaf", "type":"trojan", "server":"host", "server_port":443,
		"password":"secret", "tls":{"enabled":true,"server_name":"tls.example"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got["servername"]) != `"tls.example"` || string(got["tls"]) != `true` || got["sni"] != nil {
		t.Fatalf("unexpected Trojan TLS options: %v", got)
	}
}
