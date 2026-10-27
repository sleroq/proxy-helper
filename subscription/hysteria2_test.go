package subscription

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNativeHysteria2(t *testing.T) {
	for _, tt := range []struct {
		name, uri, want string
	}{
		{
			"default port and TLS", "hysteria2://secret@example.com#hy2",
			`{"type":"hysteria2","tag":"hy2","server":"example.com","server_port":443,"password":"secret","tls":{"enabled":true}}`,
		},
		{
			"alias and IPv6", "hy2://secret@[2001:db8::1]:8443/",
			`{"type":"hysteria2","tag":"2001:db8::1","server":"2001:db8::1","server_port":8443,"password":"secret","tls":{"enabled":true}}`,
		},
		{
			"userpass", "hysteria2://user:p%40ss%3Aword@example.com#userpass",
			`{"type":"hysteria2","tag":"userpass","server":"example.com","server_port":443,"password":"user:p@ss:word","tls":{"enabled":true}}`,
		},
		{
			"escaped auth and name", "HY2://p%3Aa%25%2B%40@example.com#Warsaw%20%2B%20%2520",
			`{"type":"hysteria2","tag":"Warsaw + %20","server":"example.com","server_port":443,"password":"p:a%+@","tls":{"enabled":true}}`,
		},
		{
			"optional auth", "hysteria2://example.com",
			`{"type":"hysteria2","tag":"example.com","server":"example.com","server_port":443,"password":"","tls":{"enabled":true}}`,
		},
		{
			"TLS and salamander", "hy2://secret@example.com:8443/?sni=cdn.example.com&insecure=1&alpn=h3,h3-29&obfs=salamander&obfs-password=obfs%2Bpass#obfs",
			`{"type":"hysteria2","tag":"obfs","server":"example.com","server_port":8443,"password":"secret","tls":{"enabled":true,"server_name":"cdn.example.com","insecure":true,"alpn":["h3","h3-29"]},"obfs":{"type":"salamander","password":"obfs+pass"}}`,
		},
		{
			"explicit secure TLS", "hysteria2://secret@example.com?insecure=0",
			`{"type":"hysteria2","tag":"example.com","server":"example.com","server_port":443,"password":"secret","tls":{"enabled":true,"insecure":false}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := (Native{}).Parse(strings.NewReader(tt.uri), Source{})
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 1 {
				t.Fatalf("got %d nodes, want 1", len(nodes))
			}

			data, err := json.Marshal(nodes[0])
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s; want %s", data, tt.want)
			}
		})
	}
}

func TestNativeHysteria2RejectsUnsupported(t *testing.T) {
	for _, uri := range []string{
		"hy2://secret@:443",
		"hy2://secret@example.com:0",
		"hy2://secret@example.com:65536",
		"hy2://secret@0.0.0.0:443",
		"hy2://secret@example.com:443,8443",
		"hy2://secret@example.com:5000-6000",
		"hy2://secret@example.com/path",
		"hy2://secret@example.com?insecure=true",
		"hy2://secret@example.com?insecure=",
		"hy2://secret@example.com?insecure=2",
		"hy2://secret@example.com?obfs=unknown&obfs-password=secret",
		"hy2://secret@example.com?obfs=gecko&obfs-password=secret",
		"hy2://secret@example.com?obfs=salamander",
		"hy2://secret@example.com?obfs-password=secret",
		"hy2://secret@example.com?pinSHA256=abcdef",
		"hy2://secret@example.com?ech=abcdef",
		"hy2://secret@example.com?mport=443,8443",
		"hy2://secret@example.com?insecure=1&bad=%zz",
		"hy2://secret@example.com?insecure=1;obfs=salamander",
	} {
		t.Run(uri, func(t *testing.T) {
			if _, err := (Native{}).Parse(strings.NewReader(uri), Source{}); err == nil {
				t.Fatal("expected unsupported or invalid URI to be rejected")
			}
		})
	}
}

func TestNativeHysteria2Subscription(t *testing.T) {
	body := "trojan://secret@example.com:443#old\n" +
		"hysteria2://secret@example.com#Warsaw\n" +
		"hy2://secret@example.org#Warsaw\n"
	for _, encoded := range []bool{false, true} {
		text := body
		if encoded {
			text = base64.StdEncoding.EncodeToString([]byte(body))
		}
		nodes, err := (Native{}).Parse(strings.NewReader(text), Source{Prefix: "cw-"})
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 3 || nodes[1].String("tag") != "cw-Warsaw" || nodes[2].String("tag") != "cw-Warsaw-2" {
			t.Fatalf("mixed subscription lost Hysteria 2 nodes: %v", nodes)
		}
	}
}

func TestNativeHysteria2Exclusions(t *testing.T) {
	body := "trojan://secret@example.com:443#old\n" +
		"hysteria2://secret@example.com#Warsaw\n" +
		"hy2://secret@example.org#Warsaw\n"
	for _, source := range []Source{
		{ExcludeProtocols: "hysteria2"},
		{ExcludeProtocols: "hy2"},
		{ExcludeProtocols: " HY2,ss "},
		{ExcludeNodeNames: "Warsaw"},
	} {
		nodes, err := (Native{}).Parse(strings.NewReader(body), source)
		if err != nil || len(nodes) != 1 || nodes[0].String("type") != "trojan" {
			t.Fatalf("exclude %+v: %v, %v", source, nodes, err)
		}
	}
}
