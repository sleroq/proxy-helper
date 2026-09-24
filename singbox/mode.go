package singbox

import (
	"encoding/json"
	"fmt"
)

func applyMode(root map[string]json.RawMessage, options Options) error {
	if err := applyTunnel(root, options.TunnelOff, options.TunnelMode); err != nil {
		return err
	}
	if !options.Bypass {
		return nil
	}
	return applyBypass(root)
}

func applyTunnel(root map[string]json.RawMessage, off, require bool) error {
	var inbounds []map[string]json.RawMessage
	if err := json.Unmarshal(root["inbounds"], &inbounds); err != nil && len(root["inbounds"]) != 0 {
		return fmt.Errorf("invalid template inbounds")
	}
	tun := false
	kept := make([]map[string]json.RawMessage, 0, len(inbounds))
	for _, inbound := range inbounds {
		var kind string
		if err := json.Unmarshal(inbound["type"], &kind); err != nil {
			return fmt.Errorf("invalid inbound type")
		}
		if kind == "tun" {
			tun = true
			if off {
				continue
			}
		}
		kept = append(kept, inbound)
	}
	if require && !tun {
		return fmt.Errorf("template has no TUN inbound")
	}
	if off {
		root["inbounds"], _ = json.Marshal(kept)
	}
	return nil
}

func applyBypass(root map[string]json.RawMessage) error {
	var route map[string]json.RawMessage
	if len(root["route"]) != 0 {
		if err := json.Unmarshal(root["route"], &route); err != nil || route == nil {
			return fmt.Errorf("invalid template route")
		}
	} else {
		route = map[string]json.RawMessage{}
	}
	var rules []map[string]json.RawMessage
	if len(route["rules"]) != 0 {
		if err := json.Unmarshal(route["rules"], &rules); err != nil {
			return fmt.Errorf("invalid route rules")
		}
	}
	for _, rule := range rules {
		if err := directRule(rule); err != nil {
			return err
		}
	}
	route["rules"], _ = json.Marshal(rules)
	route["final"] = field("direct")
	var dns map[string]json.RawMessage
	if len(root["dns"]) != 0 {
		if err := json.Unmarshal(root["dns"], &dns); err != nil || dns == nil {
			return fmt.Errorf("invalid template DNS")
		}
	} else {
		dns = map[string]json.RawMessage{}
	}
	var servers []map[string]json.RawMessage
	if len(dns["servers"]) != 0 {
		if err := json.Unmarshal(dns["servers"], &servers); err != nil {
			return fmt.Errorf("invalid DNS servers")
		}
	}
	local := ""
	var localServer map[string]json.RawMessage
	for _, server := range servers {
		var kind, tag string
		_ = json.Unmarshal(server["type"], &kind)
		_ = json.Unmarshal(server["tag"], &tag)
		if kind == "local" && tag != "" {
			local, localServer = tag, server
			break
		}
	}
	if local == "" {
		local = "sb-local"
		for _, server := range servers {
			var tag string
			_ = json.Unmarshal(server["tag"], &tag)
			if tag == local {
				return fmt.Errorf("local DNS tag collision")
			}
		}
		localServer = map[string]json.RawMessage{"type": field("local"), "tag": field(local)}
	}
	// Do not instantiate unused remote resolvers with a proxy detour in bypass.
	dns["servers"], _ = json.Marshal([]map[string]json.RawMessage{localServer})
	// Remote DNS policy rules must not redirect bypass queries through a proxy.
	dns["rules"] = json.RawMessage(`[]`)
	dns["final"] = field(local)
	route["default_domain_resolver"] = field(local)
	root["dns"], _ = json.Marshal(dns)
	root["route"], _ = json.Marshal(route)
	return nil
}

func directRule(rule map[string]json.RawMessage) error {
	var action, outbound, kind string
	_ = json.Unmarshal(rule["action"], &action)
	_ = json.Unmarshal(rule["outbound"], &outbound)
	_ = json.Unmarshal(rule["type"], &kind)
	if kind == "logical" {
		var nested []map[string]json.RawMessage
		if err := json.Unmarshal(rule["rules"], &nested); err != nil || len(nested) == 0 {
			return fmt.Errorf("unsupported logical route rule")
		}
		for _, child := range nested {
			if err := directRule(child); err != nil {
				return err
			}
		}
		rule["rules"], _ = json.Marshal(nested)
	}
	if action != "" && action != "route" && action != "sniff" && action != "hijack-dns" {
		return fmt.Errorf("unsupported bypass route action")
	}
	if outbound != "" && outbound != "direct" && outbound != "proxy" {
		return fmt.Errorf("unsupported bypass route outbound")
	}
	if outbound == "proxy" {
		rule["outbound"] = field("direct")
	}
	if action == "route" && outbound == "" {
		return fmt.Errorf("unsupported bypass route without outbound")
	}
	return nil
}
