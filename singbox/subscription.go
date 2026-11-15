package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
)

const subscriptionDNS = "ph-subscription-local"

// EnsureSubscriptionBypass adds direct DNS and routing for subscription hosts.
// Existing rules are retained so an installed configuration can be updated in place.
func EnsureSubscriptionBypass(config json.RawMessage, hosts []string) (json.RawMessage, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil || root == nil {
		return nil, false, fmt.Errorf("config must be a JSON object")
	}

	if len(hosts) == 0 {
		return config, false, nil
	}

	domains, ips := subscriptionHosts(hosts)

	route, err := subscriptionSection(root, "route")
	if err != nil {
		return nil, false, err
	}
	routeRules, err := subscriptionRules(route, "route")
	if err != nil {
		return nil, false, err
	}
	dnsChanged, err := addSubscriptionDNS(root, domains)
	if err != nil {
		return nil, false, err
	}
	routeChanged := addSubscriptionRoutes(route, routeRules, domains, ips)
	if !dnsChanged && !routeChanged {
		return config, false, nil
	}
	root["route"], _ = json.Marshal(route)
	result, err := json.MarshalIndent(root, "", "  ")
	return result, true, err
}

func subscriptionHosts(hosts []string) ([]string, []string) {
	var domains, ips []string
	for _, host := range hosts {
		if ip, err := netip.ParseAddr(host); err == nil {
			bits := 128
			if ip.Is4() {
				bits = 32
			}
			ips = append(ips, netip.PrefixFrom(ip, bits).String())
		} else {
			domains = append(domains, host)
		}
	}

	return domains, ips
}

func addSubscriptionRoutes(route map[string]json.RawMessage, routeRules []map[string]json.RawMessage, domains, ips []string) bool {
	changed := false
	if len(domains) > 0 {
		sniff := map[string]json.RawMessage{"action": field("sniff")}
		if len(routeRules) == 0 || !sameRule(routeRules[0], sniff) {
			routeRules = append([]map[string]json.RawMessage{sniff}, routeRules...)
			changed = true
		}
	}

	index := 0
	if len(routeRules) > 0 && sameRule(routeRules[0], map[string]json.RawMessage{"action": field("sniff")}) {
		index = 1
	}
	covered := priorityRules(routeRules[index:], false)
	added := missingSubscriptionRoutes(covered, nil, "domain", domains)
	added = missingSubscriptionRoutes(covered, added, "ip_cidr", ips)
	if changed || len(added) > 0 {
		routeRules = slices.Insert(routeRules, index, added...)
		route["rules"], _ = json.Marshal(routeRules)
		return true
	}
	return false
}

func missingSubscriptionRoutes(covered, added []map[string]json.RawMessage, key string, hosts []string) []map[string]json.RawMessage {
	for _, host := range hosts {
		rule := map[string]json.RawMessage{key: field([]string{host}), "outbound": field("direct")}
		if !containsRule(covered, rule) && !containsRule(added, rule) {
			added = append(added, rule)
		}
	}
	return added
}

func addSubscriptionDNS(root map[string]json.RawMessage, domains []string) (bool, error) {
	changed := false
	dns, err := subscriptionSection(root, "dns")
	if err != nil {
		return false, err
	}
	dnsRules, err := subscriptionRules(dns, "DNS")
	if err != nil {
		return false, err
	}

	changed, err = addSubscriptionResolver(dns)
	if err != nil {
		return false, err
	}

	for _, domain := range domains {
		rule := map[string]json.RawMessage{"domain": field([]string{domain}), "server": field(subscriptionDNS)}
		if !containsRule(priorityRules(dnsRules, true), rule) {
			dnsRules = append([]map[string]json.RawMessage{rule}, dnsRules...)
			changed = true
		}
	}
	if len(domains) > 0 {
		dns["rules"], _ = json.Marshal(dnsRules)
	}
	root["dns"], _ = json.Marshal(dns)
	return changed, nil
}

func addSubscriptionResolver(dns map[string]json.RawMessage) (bool, error) {
	changed := false
	var servers []map[string]json.RawMessage
	if len(dns["servers"]) > 0 {
		if err := json.Unmarshal(dns["servers"], &servers); err != nil {
			return false, fmt.Errorf("invalid DNS servers")
		}
	}

	found := false
	for _, server := range servers {
		var tag string
		_ = json.Unmarshal(server["tag"], &tag)
		if tag == subscriptionDNS {
			expected := map[string]json.RawMessage{"type": field("local"), "tag": field(subscriptionDNS)}
			if !sameRule(server, expected) {
				return false, fmt.Errorf("subscription DNS tag collision")
			}
			found = true
		}
	}
	if !found {
		servers = append(servers, map[string]json.RawMessage{"type": field("local"), "tag": field(subscriptionDNS)})
		dns["servers"], _ = json.Marshal(servers)
		changed = true
	}

	return changed, nil
}

// HasSubscriptionBypass reports whether the reserved resolver identifying managed
// subscription rules is present, including when only IP hosts were installed.
func HasSubscriptionBypass(config json.RawMessage) (bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil || root == nil {
		return false, fmt.Errorf("config must be a JSON object")
	}
	dns, err := subscriptionSection(root, "dns")
	if err != nil {
		return false, err
	}
	var servers []map[string]json.RawMessage
	if len(dns["servers"]) > 0 {
		if err := json.Unmarshal(dns["servers"], &servers); err != nil {
			return false, fmt.Errorf("invalid DNS servers")
		}
	}
	for _, server := range servers {
		var tag string
		_ = json.Unmarshal(server["tag"], &tag)
		if tag == subscriptionDNS {
			if !sameRule(server, map[string]json.RawMessage{"type": field("local"), "tag": field(subscriptionDNS)}) {
				return false, fmt.Errorf("subscription DNS tag collision")
			}
			return true, nil
		}
	}
	return false, nil
}

func subscriptionSection(root map[string]json.RawMessage, name string) (map[string]json.RawMessage, error) {
	section := map[string]json.RawMessage{}
	if len(root[name]) > 0 {
		if err := json.Unmarshal(root[name], &section); err != nil || section == nil {
			return nil, fmt.Errorf("invalid %s", name)
		}
	}
	return section, nil
}

func subscriptionRules(section map[string]json.RawMessage, name string) ([]map[string]json.RawMessage, error) {
	var rules []map[string]json.RawMessage
	if len(section["rules"]) > 0 {
		if err := json.Unmarshal(section["rules"], &rules); err != nil {
			return nil, fmt.Errorf("invalid %s rules", name)
		}
	}
	return rules, nil
}

func containsRule(rules []map[string]json.RawMessage, rule map[string]json.RawMessage) bool {
	for _, candidate := range rules {
		if sameRule(candidate, rule) {
			return true
		}
	}
	return false
}

func sameRule(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		var left, right bytes.Buffer
		if json.Compact(&left, value) != nil || json.Compact(&right, b[key]) != nil || !bytes.Equal(left.Bytes(), right.Bytes()) {
			return false
		}
	}
	return true
}

// Only rules before other policy can establish coverage: later direct rules may
// never be reached when an earlier rule routes the same destination elsewhere.
func priorityRules(rules []map[string]json.RawMessage, dns bool) []map[string]json.RawMessage {
	for i, rule := range rules {
		var target string
		key := "outbound"
		expected := "direct"
		if dns {
			key = "server"
			expected = subscriptionDNS
		}
		_ = json.Unmarshal(rule[key], &target)
		if len(rule) != 2 || target != expected || (len(rule["domain"]) == 0 && (dns || len(rule["ip_cidr"]) == 0)) {
			return rules[:i]
		}
	}
	return rules
}
