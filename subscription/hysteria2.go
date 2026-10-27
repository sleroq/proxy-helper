package subscription

import (
	"fmt"
	"net/url"

	"github.com/sleroq/ph/proxy"
)

func nativeHysteria2(node proxy.Node, u *url.URL) error {
	q, err := nativeHysteria2Query(u)
	if err != nil {
		return err
	}

	portText := u.Port()
	if portText == "" {
		portText = "443"
	}
	port, err := nativePort(u.Hostname(), portText)
	if err != nil {
		return err
	}
	nativeField(node, "server", u.Hostname())
	nativeField(node, "server_port", port)
	password := ""
	if u.User != nil {
		password = u.User.Username()
		if secret, ok := u.User.Password(); ok {
			password += ":" + secret
		}
	}
	nativeField(node, "password", password)
	return nativeHysteria2Security(node, q)
}

func nativeHysteria2Query(u *url.URL) (url.Values, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid Hysteria 2 query")
	}
	for key := range q {
		switch key {
		case "sni", "alpn", "insecure", "obfs", "obfs-password":
		default:
			return nil, fmt.Errorf("unsupported Hysteria 2 option")
		}
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("unsupported Hysteria 2 path")
	}
	return q, nil
}

func nativeHysteria2Security(node proxy.Node, q url.Values) error {
	tls := nativeTLS(q.Get("sni"), q.Get("alpn"))
	if q.Has("insecure") {
		switch q.Get("insecure") {
		case "0", "1":
			tls["insecure"] = q.Get("insecure") == "1"
		default:
			return fmt.Errorf("invalid Hysteria 2 insecure flag")
		}
	}
	nativeField(node, "tls", tls)

	switch q.Get("obfs") {
	case "":
		if q.Has("obfs-password") {
			return fmt.Errorf("missing Hysteria 2 obfuscation type")
		}
	case "salamander":
		if q.Get("obfs-password") == "" {
			return fmt.Errorf("missing Hysteria 2 obfuscation password")
		}
		nativeField(node, "obfs", map[string]string{"type": "salamander", "password": q.Get("obfs-password")})
	default:
		return fmt.Errorf("unsupported Hysteria 2 obfuscation")
	}
	return nil
}
