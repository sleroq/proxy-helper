package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"slices"
)

const (
	subscriptionDNS    = "ph-subscription-local"
	subscriptionDirect = "ph-subscription-direct"
)

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

	changed, err := configureSubscriptionBypass(root, hosts)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return config, false, nil
	}
	result, err := json.MarshalIndent(root, "", "  ")
	return result, true, err
}

func configureSubscriptionBypass(root map[string]json.RawMessage, hosts []string) (bool, error) {
	domains, ips := subscriptionHosts(hosts)
	route, err := subscriptionSection(root, "route")
	if err != nil {
		return false, err
	}
	routeRules, err := subscriptionRules(route, "route")
	if err != nil {
		return false, err
	}
	outboundChanged, err := addSubscriptionOutbound(root, len(domains) > 0)
	if err != nil {
		return false, err
	}
	dnsChanged, err := addSubscriptionDNS(root, route, domains)
	if err != nil {
		return false, err
	}
	routeChanged := addSubscriptionRoutes(route, routeRules, domains, ips)
	if !outboundChanged && !dnsChanged && !routeChanged {
		return false, nil
	}
	root["route"], _ = json.Marshal(route)
	return true, nil
}

func addSubscriptionOutbound(root map[string]json.RawMessage, domains bool) (bool, error) {
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(root["outbounds"], &outbounds); err != nil {
		return false, fmt.Errorf("invalid outbounds")
	}
	direct, managed, err := subscriptionOutbounds(outbounds)
	if err != nil {
		return false, err
	}
	expected, err := expectedSubscriptionOutbound(direct, managed)
	if err != nil {
		return false, err
	}

	if domains {
		expected["domain_resolver"] = field(subscriptionDNS)
	}
	if managed != nil && sameRule(managed, expected) {
		return false, nil
	}
	if managed == nil {
		outbounds = append(outbounds, expected)
	} else {
		clear(managed)
		maps.Copy(managed, expected)
	}
	root["outbounds"], _ = json.Marshal(outbounds)
	return true, nil
}

func subscriptionOutbounds(outbounds []map[string]json.RawMessage) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	var direct, managed map[string]json.RawMessage
	for _, outbound := range outbounds {
		var tag string
		_ = json.Unmarshal(outbound["tag"], &tag)
		switch tag {
		case "direct":
			direct = outbound
		case subscriptionDirect:
			if managed != nil {
				return nil, nil, fmt.Errorf("subscription outbound tag collision")
			}
			managed = outbound
		}
	}
	return direct, managed, nil
}

func expectedSubscriptionOutbound(direct, managed map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if direct == nil {
		return nil, fmt.Errorf("missing direct outbound")
	}
	var kind string
	_ = json.Unmarshal(direct["type"], &kind)
	if kind != "direct" {
		return nil, fmt.Errorf("invalid direct outbound")
	}

	expected := maps.Clone(direct)
	expected["tag"] = field(subscriptionDirect)
	if managed != nil && !sameRule(managed, expected) {
		// A previously installed domain resolver remains in place on IP-only updates.
		expected["domain_resolver"] = field(subscriptionDNS)
		if !sameRule(managed, expected) {
			return nil, fmt.Errorf("subscription outbound tag collision")
		}
	}
	return expected, nil
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
		rule := map[string]json.RawMessage{key: field([]string{host}), "outbound": field(subscriptionDirect)}
		if !containsRule(covered, rule) && !containsRule(added, rule) {
			added = append(added, rule)
		}
	}
	return added
}

func addSubscriptionDNS(root, route map[string]json.RawMessage, domains []string) (bool, error) {
	changed := false

	dns, err := subscriptionSection(root, "dns")
	if err != nil {
		return false, err
	}
	dnsRules, err := subscriptionRules(dns, "DNS")
	if err != nil {
		return false, err
	}

	changed, err = addSubscriptionResolver(dns, route, len(domains) > 0)
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
	if changed {
		root["dns"], _ = json.Marshal(dns)
	}
	return changed, nil
}

func addSubscriptionResolver(dns, route map[string]json.RawMessage, add bool) (bool, error) {
	servers, found, err := subscriptionDNSServers(dns)
	if err != nil {
		return false, err
	}
	if found || !add {
		return false, nil
	}

	if len(servers) == 1 && len(route["default_domain_resolver"]) == 0 {
		var tag string
		_ = json.Unmarshal(servers[0]["tag"], &tag)
		if tag == "" {
			tag = "ph-subscription-previous-dns"
			servers[0]["tag"] = field(tag)
		}
		route["default_domain_resolver"] = field(tag)
	}
	servers = append(servers, map[string]json.RawMessage{"type": field("local"), "tag": field(subscriptionDNS)})
	dns["servers"], _ = json.Marshal(servers)
	return true, nil
}

func subscriptionDNSServers(dns map[string]json.RawMessage) ([]map[string]json.RawMessage, bool, error) {
	var servers []map[string]json.RawMessage
	if len(dns["servers"]) > 0 {
		if err := json.Unmarshal(dns["servers"], &servers); err != nil {
			return nil, false, fmt.Errorf("invalid DNS servers")
		}
	}
	found := false
	for _, server := range servers {
		var tag string
		_ = json.Unmarshal(server["tag"], &tag)
		if tag == subscriptionDNS {
			expected := map[string]json.RawMessage{"type": field("local"), "tag": field(subscriptionDNS)}
			if !sameRule(server, expected) {
				return nil, false, fmt.Errorf("subscription DNS tag collision")
			}
			found = true
		}
	}
	return servers, found, nil
}

// HasSubscriptionBypass recognizes current and legacy subscription bypass configurations.
func HasSubscriptionBypass(config json.RawMessage) (bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil || root == nil {
		return false, fmt.Errorf("config must be a JSON object")
	}
	var outbounds []map[string]json.RawMessage
	if len(root["outbounds"]) > 0 {
		if err := json.Unmarshal(root["outbounds"], &outbounds); err != nil {
			return false, fmt.Errorf("invalid outbounds")
		}
	}
	direct, managed, err := subscriptionOutbounds(outbounds)
	if err != nil {
		return false, err
	}
	if managed != nil {
		_, err := expectedSubscriptionOutbound(direct, managed)
		return err == nil, err
	}
	dns, err := subscriptionSection(root, "dns")
	if err != nil {
		return false, err
	}
	_, found, err := subscriptionDNSServers(dns)
	return found, err
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
		expected := subscriptionDirect
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
