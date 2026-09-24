package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sleroq/sb/singbox"
)

// Native fetches and parses supported URI subscriptions without an external converter.
type Native struct{ Client *http.Client }

const nativeLimit = 8 << 20

func (n Native) Fetch(ctx context.Context, source Source) ([]singbox.Outbound, error) {
	address, err := sourceAddress(source)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("source %s: invalid request", source.ID)
	}
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	copyClient := *client
	prior := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("invalid redirect scheme")
		}
		if prior != nil {
			return prior(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("source %s: fetch failed", source.ID)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s: HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > nativeLimit {
		return nil, fmt.Errorf("source %s: subscription response too large", source.ID)
	}
	return n.Parse(response.Body, source)
}

func nativeDecode(s string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(s); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("invalid encoding")
}

func nativeField(node singbox.Outbound, key string, value any) {
	data, _ := json.Marshal(value)
	node[key] = data
}

func (Native) Parse(body io.Reader, source Source) ([]singbox.Outbound, error) {
	data, err := io.ReadAll(io.LimitReader(body, nativeLimit+1))
	if err != nil {
		return nil, fmt.Errorf("source %s: cannot read response", source.ID)
	}
	if len(data) > nativeLimit {
		return nil, fmt.Errorf("source %s: subscription response too large", source.ID)
	}
	text := strings.TrimSpace(string(data))
	if !strings.Contains(text, "://") {
		if decoded, e := nativeDecode(strings.Join(strings.Fields(text), "")); e == nil {
			text = string(decoded)
		}
	}
	var excluded *regexp.Regexp
	if source.ExcludeNodeNames != "" {
		excluded, err = regexp.Compile(source.ExcludeNodeNames)
		if err != nil {
			return nil, fmt.Errorf("source %s: invalid node exclusion pattern", source.ID)
		}
	}
	protocols := map[string]bool{}
	for _, p := range strings.Split(source.ExcludeProtocols, ",") {
		protocols[strings.ToLower(strings.TrimSpace(p))] = true
	}
	nodes := []singbox.Outbound{}
	tags := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		node, name, e := nativeLine(line)
		if e != nil || protocols[node.String("type")] || (excluded != nil && excluded.MatchString(name)) {
			continue
		}
		tag := source.Prefix + name
		if tag == "" {
			tag = source.Prefix + node.String("server")
		}
		base := tag
		for i := 2; tags[tag]; i++ {
			tag = fmt.Sprintf("%s-%d", base, i)
		}
		tags[tag] = true
		nativeField(node, "tag", tag)
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("source %s: no supported outbounds", source.ID)
	}
	return nodes, nil
}

func nativeLine(line string) (singbox.Outbound, string, error) {
	u, err := url.Parse(line)
	if err != nil {
		return nil, "", err
	}
	kind := strings.ToLower(u.Scheme)
	if kind != "ss" && kind != "trojan" && kind != "vless" && kind != "vmess" {
		return nil, "", fmt.Errorf("unsupported")
	}
	name, _ := url.QueryUnescape(u.Fragment)
	node := singbox.Outbound{}
	nativeField(node, "type", kind)
	if kind == "vmess" {
		encoded := strings.TrimPrefix(line, "vmess://")
		encoded = strings.SplitN(encoded, "#", 2)[0]
		data, e := nativeDecode(encoded)
		if e != nil {
			return nil, "", e
		}
		var v struct {
			Address  string      `json:"add"`
			Port     json.Number `json:"port"`
			ID       string      `json:"id"`
			Name     string      `json:"ps"`
			Security string      `json:"scy"`
			Network  string      `json:"net"`
			Host     string      `json:"host"`
			Path     string      `json:"path"`
			TLS      string      `json:"tls"`
			SNI      string      `json:"sni"`
			ALPN     string      `json:"alpn"`
		}
		if err = json.Unmarshal(data, &v); err != nil {
			return nil, "", err
		}
		if name == "" {
			name = v.Name
		}
		port, e := strconv.Atoi(string(v.Port))
		if e != nil || port < 1 || port > 65535 || v.ID == "" || v.Address == "" {
			return nil, "", fmt.Errorf("invalid node")
		}
		nativeField(node, "server", v.Address)
		nativeField(node, "server_port", port)
		nativeField(node, "uuid", v.ID)
		if v.Security != "" {
			nativeField(node, "security", v.Security)
		} else {
			nativeField(node, "security", "auto")
		}
		nativeTransport(node, v.Network, v.Host, v.Path)
		if v.TLS == "tls" {
			tls := map[string]any{"enabled": true}
			if v.SNI != "" {
				tls["server_name"] = v.SNI
			}
			if v.ALPN != "" {
				tls["alpn"] = strings.Split(v.ALPN, ",")
			}
			nativeField(node, "tls", tls)
		}
	} else {
		authority := u.Host
		user := ""
		if u.User != nil {
			user = u.User.String()
		}
		if kind == "ss" {
			if user == "" {
				raw := strings.SplitN(authority, "@", 2)
				if len(raw) != 2 {
					return nil, "", fmt.Errorf("invalid node")
				}
				decoded, e := nativeDecode(raw[0])
				if e != nil {
					return nil, "", e
				}
				user = string(decoded)
				authority = raw[1]
			} else if !strings.Contains(user, ":") {
				decoded, e := nativeDecode(user)
				if e != nil {
					return nil, "", e
				}
				user = string(decoded)
			}
			pair := strings.SplitN(user, ":", 2)
			if len(pair) != 2 || pair[0] == "" || pair[1] == "" {
				return nil, "", fmt.Errorf("invalid node")
			}
			nativeField(node, "method", pair[0])
			nativeField(node, "password", pair[1])
		} else if kind == "trojan" {
			if u.User == nil {
				return nil, "", fmt.Errorf("invalid node")
			}
			password, _ := u.User.Password()
			if password == "" {
				password = u.User.Username()
			}
			if password == "" {
				return nil, "", fmt.Errorf("invalid node")
			}
			nativeField(node, "password", password)
		} else {
			if u.User == nil || u.User.Username() == "" {
				return nil, "", fmt.Errorf("invalid node")
			}
			nativeField(node, "uuid", u.User.Username())
			if flow := u.Query().Get("flow"); flow != "" {
				nativeField(node, "flow", flow)
			}
		}
		host, portText := u.Hostname(), u.Port()
		if kind == "ss" {
			parsed, e := url.Parse("ss://" + authority)
			if e != nil {
				return nil, "", e
			}
			host = parsed.Hostname()
			portText = parsed.Port()
		}
		port, e := strconv.Atoi(portText)
		if e != nil || port < 1 || port > 65535 || host == "" || host == "0.0.0.0" || host == "::" {
			return nil, "", fmt.Errorf("invalid node")
		}
		nativeField(node, "server", host)
		nativeField(node, "server_port", port)
		q := u.Query()
		if kind != "ss" {
			nativeTransport(node, q.Get("type"), q.Get("host"), q.Get("path"))
			security := q.Get("security")
			if kind == "trojan" && security == "" {
				security = "tls"
			}
			if security == "tls" || security == "reality" {
				tls := map[string]any{"enabled": true}
				if s := q.Get("sni"); s != "" {
					tls["server_name"] = s
				}
				if a := q.Get("alpn"); a != "" {
					tls["alpn"] = strings.Split(a, ",")
				}
				if security == "reality" {
					if pk := q.Get("pbk"); pk != "" {
						tls["reality"] = map[string]any{"enabled": true, "public_key": pk, "short_id": q.Get("sid")}
					}
				}
				nativeField(node, "tls", tls)
			}
		}
	}
	if name == "" {
		name = node.String("server")
	}
	return node, name, nil
}

func nativeTransport(node singbox.Outbound, network, host, path string) {
	if network == "ws" {
		transport := map[string]any{"type": "ws"}
		if path != "" {
			transport["path"] = path
		}
		if host != "" {
			transport["headers"] = map[string]string{"Host": host}
		}
		nativeField(node, "transport", transport)
	}
	if network == "grpc" {
		nativeField(node, "transport", map[string]any{"type": "grpc", "service_name": path})
	}
}
