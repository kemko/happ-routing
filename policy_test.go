package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

func writePolicy(t *testing.T, path string, policy routingPolicy) {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCompiledPolicyMatchesExports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.json")
	policy := routingPolicy{
		DirectSites: []string{"domain:alib.ru", "domain:alib.ru"},
		ProxySites:  []string{"geosite:service", "domain:manual.example", "full:exact.example"},
		DirectIP:    []string{"192.168.0.7/16"}, ProxyIP: []string{"geoip:service", "2001:db8::1"},
		RouteOrder: "direct-proxy-block", GlobalProxy: "false", DomainStrategy: "IPIfNonMatch",
	}
	writePolicy(t, path, policy)
	domains, err := parseDomains([]byte("+.service.example\nfull.other.example\n"), "mihomo")
	if err != nil {
		t.Fatal(err)
	}
	networks, err := parseIPs([]byte("198.51.100.8/24\n"))
	if err != nil {
		t.Fatal(err)
	}
	sites := &routercommon.GeoSiteList{Entry: []*routercommon.GeoSite{{CountryCode: "SERVICE", Domain: domains}}}
	ips := &routercommon.GeoIPList{Entry: []*routercommon.GeoIP{{CountryCode: "SERVICE", Cidr: networks}}}
	files, err := compilePolicy(path, sites, ips)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["mihomo/direct-domains.txt"]) != "+.alib.ru\n" || string(files["mihomo/direct-ips.txt"]) != "192.168.0.0/16\n" {
		t.Fatalf("exceptions are not normalized/deduplicated: %q", files)
	}
	if !strings.Contains(string(files["mihomo/proxy-domains.txt"]), "+.manual.example\n") || !strings.Contains(string(files["mihomo/proxy-domains.txt"]), "\nexact.example\n") {
		t.Fatalf("manual domain semantics lost: %q", files["mihomo/proxy-domains.txt"])
	}
	for index, action := range []string{"direct", "proxy"} {
		gotDomains, err := parseDomains(files["mihomo/"+action+"-domains.txt"], "mihomo")
		if err != nil || !reflect.DeepEqual(gotDomains, sites.Entry[index+1].Domain) {
			t.Fatalf("DAT/text domains differ: %v", err)
		}
		gotIPs, err := parseIPs(files["mihomo/"+action+"-ips.txt"])
		if err != nil || !reflect.DeepEqual(gotIPs, ips.Entry[index+1].Cidr) {
			t.Fatalf("DAT/text IPs differ: %v", err)
		}
		if sites.Entry[index+1].CountryCode != strings.ToUpper("routing-"+action) || ips.Entry[index+1].CountryCode != strings.ToUpper("routing-"+action) {
			t.Fatal("unstable aggregate category name")
		}
	}
	if sites.Entry[0].CountryCode != "SERVICE" || len(sites.Entry[0].Domain) != 2 {
		t.Fatal("source category was changed")
	}
	// Run the official converter when available (installed in CI).
	mihomo, err := exec.LookPath("mihomo")
	if err != nil {
		t.Log("MRS round trip skipped: mihomo is not on PATH")
		return
	}
	for name, data := range files {
		input := filepath.Join(root, filepath.Base(name))
		if err := os.WriteFile(input, data, 0644); err != nil {
			t.Fatal(err)
		}
		kind := "domain"
		if strings.HasSuffix(name, "-ips.txt") {
			kind = "ipcidr"
		}
		for _, args := range [][]string{
			{"convert-ruleset", kind, "text", input, input + ".mrs"},
			{"convert-ruleset", kind, "mrs", input + ".mrs", input + ".roundtrip"},
		} {
			if output, err := exec.Command(mihomo, args...).CombinedOutput(); err != nil {
				t.Fatalf("MRS conversion: %v: %s", err, output)
			}
		}
		back, err := os.ReadFile(input + ".roundtrip")
		if err != nil {
			t.Fatal(err)
		}
		if kind == "domain" {
			got, err := parseDomains(back, "mihomo")
			want, _ := parseDomains(data, "mihomo")
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("MRS domain round trip differs: %q, %v", back, err)
			}
		} else {
			got, err := parseIPs(back)
			want, _ := parseIPs(data)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("MRS IP round trip differs: %q, %v", back, err)
			}
		}
	}
}

func TestPolicyRejectsDivergentOrInvalidRules(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*routingPolicy)
	}{
		{"missing domain category", func(p *routingPolicy) { p.ProxySites = []string{"geosite:missing"} }},
		{"missing IP category", func(p *routingPolicy) { p.ProxyIP = []string{"geoip:missing"} }},
		{"invalid domain", func(p *routingPolicy) { p.ProxySites = []string{"domain:a..com"} }},
		{"unknown rule", func(p *routingPolicy) { p.ProxySites = []string{"regexp:.*"} }},
		{"invalid IP", func(p *routingPolicy) { p.ProxyIP = []string{"198.51.100.1/33"} }},
		{"order", func(p *routingPolicy) { p.RouteOrder = "proxy-direct-block" }},
		{"DNS", func(p *routingPolicy) { p.DomainStrategy = "AsIs" }},
		{"fallback", func(p *routingPolicy) { p.GlobalProxy = "true" }},
		{"block", func(p *routingPolicy) { p.BlockSites = []string{"domain:ad.example"} }},
		{"empty set", func(p *routingPolicy) { p.DirectSites = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := routingPolicy{
				DirectSites: []string{"domain:alib.ru"}, ProxySites: []string{"domain:example.com"},
				DirectIP: []string{"192.168.0.0/16"}, ProxyIP: []string{"198.51.100.0/24"},
				RouteOrder: "direct-proxy-block", GlobalProxy: "false", DomainStrategy: "IPIfNonMatch",
			}
			test.edit(&p)
			path := filepath.Join(t.TempDir(), "policy.json")
			writePolicy(t, path, p)
			if _, err := compilePolicy(path, &routercommon.GeoSiteList{}, &routercommon.GeoIPList{}); err == nil {
				t.Fatal("invalid/unsupported policy accepted")
			}
		})
	}
}
