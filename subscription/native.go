package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sleroq/sb/proxy"
)

// Native fetches and parses supported URI subscriptions without an external converter.
type Native struct{ Client *http.Client }

const nativeLimit = 8 << 20

var errNativeTooLarge = errors.New("subscription response too large")

func (n Native) Fetch(ctx context.Context, source Source) ([]proxy.Node, error) {
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
	copyClient.CheckRedirect = nativeRedirect(client.CheckRedirect)
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("source %s: fetch failed", source.ID)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s: HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > nativeLimit {
		return nil, fmt.Errorf("source %s: %w", source.ID, errNativeTooLarge)
	}
	return n.Parse(response.Body, source)
}

func nativeRedirect(prior func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
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
}

func nativeDecode(s string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(s); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("invalid encoding")
}

func nativeField(node proxy.Node, key string, value any) {
	data, _ := json.Marshal(value)
	node[key] = data
}

func (Native) Parse(body io.Reader, source Source) ([]proxy.Node, error) {
	data, err := io.ReadAll(io.LimitReader(body, nativeLimit+1))
	if err != nil {
		return nil, fmt.Errorf("source %s: cannot read response", source.ID)
	}
	if len(data) > nativeLimit {
		return nil, fmt.Errorf("source %s: %w", source.ID, errNativeTooLarge)
	}

	text := strings.TrimSpace(string(data))
	if !strings.Contains(text, "://") {
		if decoded, e := nativeDecode(strings.Join(strings.Fields(text), "")); e == nil {
			text = string(decoded)
		}
	}

	excluded, err := nativeExclusion(source)
	if err != nil {
		return nil, err
	}
	return nativeParseLines(text, source, excluded)
}

func nativeExclusion(source Source) (*regexp.Regexp, error) {
	if source.ExcludeNodeNames == "" {
		return nil, nil
	}
	excluded, err := regexp.Compile(source.ExcludeNodeNames)
	if err != nil {
		return nil, fmt.Errorf("source %s: invalid node exclusion pattern", source.ID)
	}
	return excluded, nil
}

func nativeParseLines(text string, source Source, excluded *regexp.Regexp) ([]proxy.Node, error) {
	protocols := map[string]bool{}
	for p := range strings.SplitSeq(source.ExcludeProtocols, ",") {
		protocols[strings.ToLower(strings.TrimSpace(p))] = true
	}

	nodes := []proxy.Node{}
	tags := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		node, name, e := nativeLine(line)
		if e != nil || nativeExcluded(node, name, protocols, excluded) {
			continue
		}
		nativeTag(node, source.Prefix, name, tags)
		nodes = append(nodes, node)
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("source %s: no supported outbounds", source.ID)
	}
	return nodes, nil
}

func nativeExcluded(node proxy.Node, name string, protocols map[string]bool, excluded *regexp.Regexp) bool {
	kind := node.String("type")
	if protocols[kind] || (kind == "shadowsocks" && protocols["ss"]) {
		return true
	}
	return excluded != nil && excluded.MatchString(name)
}

func nativeTag(node proxy.Node, prefix, name string, tags map[string]bool) {
	tag := prefix + name
	if tag == "" {
		tag = prefix + node.String("server")
	}
	base := tag
	for i := 2; tags[tag]; i++ {
		tag = fmt.Sprintf("%s-%d", base, i)
	}
	tags[tag] = true
	nativeField(node, "tag", tag)
}

func nativeLine(line string) (proxy.Node, string, error) {
	line = nativeExpandSS(line)

	u, err := url.Parse(line)
	if err != nil {
		return nil, "", err
	}
	kind := strings.ToLower(u.Scheme)
	if kind != "ss" && kind != "trojan" && kind != "vless" && kind != "vmess" {
		return nil, "", fmt.Errorf("unsupported")
	}

	name, _ := url.QueryUnescape(u.Fragment)
	node := proxy.Node{}
	if kind == "ss" {
		nativeField(node, "type", "shadowsocks")
	} else {
		nativeField(node, "type", kind)
	}
	if kind == "vmess" {
		name, err = nativeVMess(node, line, name)
	} else {
		err = nativeURI(node, u, kind)
	}
	if err != nil {
		return nil, "", err
	}

	if name == "" {
		name = node.String("server")
	}
	return node, name, nil
}

func nativeExpandSS(line string) string {
	if !strings.HasPrefix(strings.ToLower(line), "ss://") {
		return line
	}
	payload := line[5:]
	encoded, _, _ := strings.Cut(payload, "#")
	encoded, _, _ = strings.Cut(encoded, "?")
	if !strings.Contains(encoded, "@") {
		if decoded, err := nativeDecode(encoded); err == nil {
			return "ss://" + string(decoded) + line[5+len(encoded):]
		}
	}
	return line
}

func nativeVMess(node proxy.Node, line, name string) (string, error) {
	encoded := strings.TrimPrefix(line, "vmess://")
	encoded = strings.SplitN(encoded, "#", 2)[0]
	data, e := nativeDecode(encoded)
	if e != nil {
		return "", e
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
	if err := json.Unmarshal(data, &v); err != nil {
		return "", err
	}
	if name == "" {
		name = v.Name
	}

	port, e := nativeVMessPort(v.Address, v.ID, v.Port)
	if e != nil {
		return "", e
	}
	nativeField(node, "server", v.Address)
	nativeField(node, "server_port", port)
	nativeField(node, "uuid", v.ID)

	security := v.Security
	if security == "" {
		security = "auto"
	}
	nativeField(node, "security", security)
	nativeTransport(node, v.Network, v.Host, v.Path)

	if v.TLS == "tls" {
		nativeField(node, "tls", nativeTLS(v.SNI, v.ALPN))
	}
	return name, nil
}

func nativeVMessPort(address, id string, portText json.Number) (int, error) {
	port, err := strconv.Atoi(string(portText))
	if err != nil || port < 1 || port > 65535 || id == "" || address == "" {
		return 0, fmt.Errorf("invalid node")
	}
	return port, nil
}

func nativeURI(node proxy.Node, u *url.URL, kind string) error {
	authority, err := nativeCredentials(node, u, kind)
	if err != nil {
		return err
	}
	host, portText := u.Hostname(), u.Port()
	if kind == "ss" {
		parsed, e := url.Parse("ss://" + authority)
		if e != nil {
			return e
		}
		host = parsed.Hostname()
		portText = parsed.Port()
	}
	port, e := nativePort(host, portText)
	if e != nil {
		return e
	}
	nativeField(node, "server", host)
	nativeField(node, "server_port", port)

	if kind == "ss" {
		return nil
	}
	return nativeURISecurity(node, u.Query(), kind)
}

func nativePort(host, portText string) (int, error) {
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || host == "" || host == "0.0.0.0" || host == "::" {
		return 0, fmt.Errorf("invalid node")
	}
	return port, nil
}

func nativeCredentials(node proxy.Node, u *url.URL, kind string) (string, error) {
	switch kind {
	case "ss":
		return nativeSSCredentials(node, u)
	case "trojan":
		if u.User == nil {
			return "", fmt.Errorf("invalid node")
		}
		password, _ := u.User.Password()
		if password == "" {
			password = u.User.Username()
		}
		if password == "" {
			return "", fmt.Errorf("invalid node")
		}
		nativeField(node, "password", password)
	default:
		if u.User == nil || u.User.Username() == "" {
			return "", fmt.Errorf("invalid node")
		}
		nativeField(node, "uuid", u.User.Username())
		if flow := u.Query().Get("flow"); flow != "" {
			nativeField(node, "flow", flow)
		}
	}
	return u.Host, nil
}

func nativeSSCredentials(node proxy.Node, u *url.URL) (string, error) {
	authority := u.Host
	user := ""
	if u.User != nil {
		user = u.User.String()
	}
	if user == "" {
		raw := strings.SplitN(authority, "@", 2)
		if len(raw) != 2 {
			return "", fmt.Errorf("invalid node")
		}
		decoded, e := nativeDecode(raw[0])
		if e != nil {
			return "", e
		}
		user = string(decoded)
		authority = raw[1]
	} else if !strings.Contains(user, ":") {
		decoded, e := nativeDecode(user)
		if e != nil {
			return "", e
		}
		user = string(decoded)
	}

	pair := strings.SplitN(user, ":", 2)
	if len(pair) != 2 || pair[0] == "" || pair[1] == "" {
		return "", fmt.Errorf("invalid node")
	}
	nativeField(node, "method", pair[0])
	nativeField(node, "password", pair[1])
	return authority, nil
}

func nativeTLS(sni, alpn string) map[string]any {
	tls := map[string]any{"enabled": true}
	if sni != "" {
		tls["server_name"] = sni
	}
	if alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	return tls
}

func nativeURISecurity(node proxy.Node, q url.Values, kind string) error {
	if network := q.Get("type"); network != "" && network != "tcp" && network != "ws" && network != "grpc" {
		return fmt.Errorf("unsupported transport")
	}
	nativeTransport(node, q.Get("type"), q.Get("host"), q.Get("path"))
	security := q.Get("security")
	if kind == "trojan" && security == "" {
		security = "tls"
	}
	if !nativeSupportedSecurity(security) {
		return fmt.Errorf("unsupported security")
	}
	if security == "tls" || security == "reality" {
		nativeField(node, "tls", nativeURITLS(q, security))
	}
	return nil
}

func nativeSupportedSecurity(security string) bool {
	return security == "" || security == "none" || security == "tls" || security == "reality"
}

func nativeURITLS(q url.Values, security string) map[string]any {
	tls := nativeTLS(q.Get("sni"), q.Get("alpn"))
	if security == "reality" {
		if pk := q.Get("pbk"); pk != "" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": pk, "short_id": q.Get("sid")}
		}
	}
	return tls
}

func nativeTransport(node proxy.Node, network, host, path string) {
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
