package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

type routingPolicy struct {
	DirectSites    []string
	ProxySites     []string
	DirectIP       []string `json:"DirectIp"`
	ProxyIP        []string `json:"ProxyIp"`
	BlockSites     []string
	BlockIP        []string `json:"BlockIp"`
	RouteOrder     string
	GlobalProxy    string
	DomainStrategy string
}

func compilePolicy(path string, sites *routercommon.GeoSiteList, ips *routercommon.GeoIPList) (map[string][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var policy routingPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("routing policy: %w", err)
	}
	// The fixed client rules share this order and DNS fallback behavior.
	if policy.RouteOrder != "direct-proxy-block" || policy.GlobalProxy != "false" || policy.DomainStrategy != "IPIfNonMatch" || len(policy.BlockSites)+len(policy.BlockIP) != 0 {
		return nil, fmt.Errorf("routing policy requires direct-proxy-block, GlobalProxy=false, IPIfNonMatch and empty block lists")
	}
	domains := make(map[string][]*routercommon.Domain)
	for _, entry := range sites.Entry {
		domains[strings.ToLower(entry.CountryCode)] = entry.Domain
	}
	networks := make(map[string][]*routercommon.CIDR)
	for _, entry := range ips.Entry {
		networks[strings.ToLower(entry.CountryCode)] = entry.Cidr
	}
	for _, tag := range []string{"routing-direct", "routing-proxy"} {
		if _, exists := domains[tag]; exists {
			return nil, fmt.Errorf("reserved source tag %q", tag)
		}
		if _, exists := networks[tag]; exists {
			return nil, fmt.Errorf("reserved source tag %q", tag)
		}
	}
	files := make(map[string][]byte)
	for _, action := range []struct {
		name       string
		sites, ips []string
	}{{"direct", policy.DirectSites, policy.DirectIP}, {"proxy", policy.ProxySites, policy.ProxyIP}} {
		domainData, err := expandDomains(action.sites, domains)
		if err != nil {
			return nil, fmt.Errorf("%s domains: %w", action.name, err)
		}
		values, err := parseDomains(domainData, "mihomo")
		if err != nil {
			return nil, fmt.Errorf("%s domains: %w", action.name, err)
		}
		ipData, err := expandIPs(action.ips, networks)
		if err != nil {
			return nil, fmt.Errorf("%s IPs: %w", action.name, err)
		}
		cidrs, err := parseIPs(ipData)
		if err != nil {
			return nil, fmt.Errorf("%s IPs: %w", action.name, err)
		}
		tag := strings.ToUpper("routing-" + action.name)
		sites.Entry = append(sites.Entry, &routercommon.GeoSite{CountryCode: tag, Domain: values})
		ips.Entry = append(ips.Entry, &routercommon.GeoIP{CountryCode: tag, Cidr: cidrs})
		files["mihomo/"+action.name+"-domains.txt"] = domainData
		files["mihomo/"+action.name+"-ips.txt"] = ipData
	}
	return files, nil
}

func expandDomains(rules []string, categories map[string][]*routercommon.Domain) ([]byte, error) {
	entries := make(map[string]bool)
	for _, rule := range rules {
		kind, value, ok := strings.Cut(rule, ":")
		if !ok || value == "" || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("unsupported domain rule %q", rule)
		}
		var values []*routercommon.Domain
		switch kind {
		case "geosite":
			var found bool
			values, found = categories[strings.ToLower(value)]
			if !found {
				return nil, fmt.Errorf("missing GeoSite category %q", value)
			}
		case "domain", "full":
			format := "suffix"
			if kind == "full" {
				format = "mihomo"
			}
			var err error
			values, err = parseDomains([]byte(value), format)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", rule, err)
			}
			if kind == "full" && values[0].Type != routercommon.Domain_Full {
				return nil, fmt.Errorf("invalid full-domain rule %q", rule)
			}
		default:
			return nil, fmt.Errorf("unsupported domain rule %q", rule)
		}
		for _, domain := range values {
			name := domain.Value
			switch domain.Type {
			case routercommon.Domain_RootDomain:
				name = "+." + name
			case routercommon.Domain_Full:
			default:
				return nil, fmt.Errorf("unsupported domain type for %q", name)
			}
			entries[name] = true
		}
	}
	return sortedEntries(entries), nil
}

func expandIPs(rules []string, categories map[string][]*routercommon.CIDR) ([]byte, error) {
	entries := make(map[string]bool)
	for _, rule := range rules {
		if strings.ContainsAny(rule, "\r\n") {
			return nil, fmt.Errorf("invalid IP rule %q", rule)
		}
		var values []*routercommon.CIDR
		if tag, ok := strings.CutPrefix(rule, "geoip:"); ok {
			var found bool
			values, found = categories[strings.ToLower(tag)]
			if !found {
				return nil, fmt.Errorf("missing GeoIP category %q", tag)
			}
		} else {
			var err error
			values, err = parseIPs([]byte(rule))
			if err != nil {
				return nil, fmt.Errorf("%q: %w", rule, err)
			}
		}
		for _, cidr := range values {
			addr, ok := netip.AddrFromSlice(cidr.Ip)
			if !ok || int(cidr.Prefix) > addr.BitLen() {
				return nil, fmt.Errorf("invalid CIDR in %q", rule)
			}
			entries[netip.PrefixFrom(addr, int(cidr.Prefix)).Masked().String()] = true
		}
	}
	return sortedEntries(entries), nil
}

func sortedEntries(entries map[string]bool) []byte {
	values := make([]string, 0, len(entries))
	for entry := range entries {
		values = append(values, entry)
	}
	sort.Strings(values)
	if len(values) == 0 {
		return nil
	}
	return []byte(strings.Join(values, "\n") + "\n")
}
