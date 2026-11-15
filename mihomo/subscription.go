package mihomo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// HasSubscriptionBypass reports whether a composed config contains managed bypass rules.
func HasSubscriptionBypass(config json.RawMessage) (bool, error) {
	var root struct {
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		return false, fmt.Errorf("invalid config: %w", err)
	}
	return slices.ContainsFunc(root.Rules, isSubscriptionRule), nil
}

func isSubscriptionRule(rule string) bool {
	parts := strings.Split(rule, ",")
	if len(parts) == 3 {
		return parts[0] == "DOMAIN" && parts[1] != "" && parts[2] == "DIRECT"
	}
	if len(parts) != 4 || parts[2] != "DIRECT" || parts[3] != "no-resolve" {
		return false
	}
	return isSubscriptionIPRule(parts[0], parts[1])
}

func isSubscriptionIPRule(kind, cidr string) bool {
	if kind != "IP-CIDR" && kind != "IP-CIDR6" {
		return false
	}
	prefix, err := netip.ParsePrefix(cidr)
	return err == nil && prefix.Bits() == prefix.Addr().BitLen() && ((kind == "IP-CIDR") == prefix.Addr().Is4())
}

// EnsureSubscriptionBypass places subscription destinations ahead of other rules.
func EnsureSubscriptionBypass(config json.RawMessage, hosts []string) (json.RawMessage, bool, error) {
	if len(hosts) == 0 {
		return config, false, nil
	}

	var root map[string]json.RawMessage
	if json.Unmarshal(config, &root) != nil || root == nil {
		return nil, false, fmt.Errorf("config must be a JSON object")
	}
	var rules []string
	if err := json.Unmarshal(root["rules"], &rules); err != nil {
		return nil, false, fmt.Errorf("invalid rules: %w", err)
	}

	bypass, domains := subscriptionRules(hosts)

	changed := len(rules) < len(bypass) || !slices.Equal(rules[:len(bypass)], bypass)
	if changed {
		remaining := slices.DeleteFunc(rules, func(rule string) bool { return slices.Contains(bypass, rule) })
		root["rules"], _ = json.Marshal(append(bypass, remaining...))
	}
	dnsChanged, err := ensureSubscriptionDNS(root, domains)
	if err != nil {
		return nil, false, err
	}
	if !changed && !dnsChanged {
		return config, false, nil
	}
	output, err := json.MarshalIndent(root, "", "  ")
	return output, true, err
}

func subscriptionRules(hosts []string) ([]string, []string) {
	var bypass, domains []string
	for _, host := range hosts {
		rule := "DOMAIN," + host + ",DIRECT"
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.Is4() {
				rule = "IP-CIDR," + host + "/32,DIRECT,no-resolve"
			} else {
				rule = "IP-CIDR6," + host + "/128,DIRECT,no-resolve"
			}
		} else if !slices.Contains(domains, host) {
			domains = append(domains, host)
		}
		if !slices.Contains(bypass, rule) {
			bypass = append(bypass, rule)
		}
	}

	return bypass, domains
}

func ensureSubscriptionDNS(root map[string]json.RawMessage, domains []string) (bool, error) {
	if len(domains) == 0 || len(root["dns"]) == 0 {
		return false, nil
	}
	dns, enabled, err := enabledSubscriptionDNS(root["dns"])
	if err != nil || !enabled {
		return false, err
	}
	policy, changed, err := subscriptionPolicy(dns["nameserver-policy"], domains)
	if err != nil {
		return false, err
	}
	if changed {
		dns["nameserver-policy"] = policy
	}
	// A configured direct resolver otherwise ignores nameserver-policy.
	if len(dns["direct-nameserver"]) > 0 && string(dns["direct-nameserver-follow-policy"]) != "true" {
		dns["direct-nameserver-follow-policy"] = json.RawMessage(`true`)
		changed = true
	}
	if changed {
		root["dns"], _ = json.Marshal(dns)
	}
	return changed, nil
}

func enabledSubscriptionDNS(raw json.RawMessage) (map[string]json.RawMessage, bool, error) {
	var dns map[string]json.RawMessage
	if json.Unmarshal(raw, &dns) != nil || dns == nil {
		return nil, false, fmt.Errorf("dns must be a JSON object")
	}
	var enabled bool
	if raw := dns["enable"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return nil, false, fmt.Errorf("invalid dns.enable: %w", err)
		}
	}
	return dns, enabled, nil
}

func subscriptionPolicy(raw json.RawMessage, domains []string) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	keys, values, err := decodeSubscriptionPolicy(raw)
	if err != nil {
		return nil, false, err
	}
	if subscriptionPolicyCovered(keys, values, domains) {
		return raw, false, nil
	}
	return prependSubscriptionPolicy(keys, values, domains), true, nil
}

func decodeSubscriptionPolicy(raw json.RawMessage) ([]string, []json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, fmt.Errorf("dns.nameserver-policy must be a JSON object")
	}

	var keys []string
	var values []json.RawMessage
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("invalid dns.nameserver-policy")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, fmt.Errorf("invalid dns.nameserver-policy")
		}
		keys = append(keys, key.(string))
		values = append(values, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, nil, fmt.Errorf("invalid dns.nameserver-policy")
	}
	return keys, values, nil
}

func subscriptionPolicyCovered(keys []string, values []json.RawMessage, domains []string) bool {
	if len(keys) < len(domains) || !slices.Equal(keys[:len(domains)], domains) {
		return false
	}
	for i := range domains {
		if string(values[i]) != `"system"` {
			return false
		}
	}
	return true
}

// Mihomo uses policy order, so keep existing entries in order behind exact hosts.
func prependSubscriptionPolicy(keys []string, values []json.RawMessage, domains []string) json.RawMessage {
	var result bytes.Buffer
	result.WriteByte('{')
	first := true
	write := func(key string, value json.RawMessage) {
		if !first {
			result.WriteByte(',')
		}
		first = false
		encoded, _ := json.Marshal(key)
		result.Write(encoded)
		result.WriteByte(':')
		result.Write(value)
	}
	for _, domain := range domains {
		write(domain, json.RawMessage(`"system"`))
	}
	for i, key := range keys {
		if !slices.Contains(domains, key) {
			write(key, values[i])
		}
	}
	result.WriteByte('}')
	return result.Bytes()
}
