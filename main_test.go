package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"
)

func TestDomains(t *testing.T) {
	got, err := parseDomains([]byte("Example.COM\n+.example.com\nexample.com\n"), "mihomo")
	if err != nil {
		t.Fatal(err)
	}
	want := []*routercommon.Domain{
		{Type: routercommon.Domain_RootDomain, Value: "example.com"},
		{Type: routercommon.Domain_Full, Value: "example.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	suffix, err := parseDomains([]byte("example.com\n"), "suffix")
	if err != nil || suffix[0].Type != routercommon.Domain_RootDomain {
		t.Fatalf("suffix: %v, %v", suffix, err)
	}
	idn, err := parseDomains([]byte("krakẹn.com\n"), "suffix")
	if err != nil || len(idn) != 1 || !strings.HasPrefix(idn[0].Value, "xn--") {
		t.Fatalf("IDN: %v, %v", idn, err)
	}
	for _, bad := range []string{"regexp:foo", "*.example.com", "https://example.com", "a..com", "example.com extra", ""} {
		if _, err := parseDomains([]byte(bad), "mihomo"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestIPs(t *testing.T) {
	got, err := parseIPs([]byte("192.0.2.7/24\n192.0.2.0/24\n2001:db8::1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0].Ip) != 16 || got[0].Prefix != 128 || len(got[1].Ip) != 4 || got[1].Prefix != 24 {
		t.Fatalf("unexpected CIDRs: %v", got)
	}
	for _, bad := range []string{"192.0.2.0/33", "192.0.2.1, 192.0.2.2", "fe80::1%eth0", ""} {
		if _, err := parseIPs([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestBuildRoundTripAndFailedSource(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch req.URL.Path {
		case "/domains":
			body = "+.example.com\nexample.org\n"
		case "/ips":
			body = "198.51.100.8/24\n2001:db8::/32\n"
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	base := "https://example.test"
	root := t.TempDir()
	configPath := filepath.Join(root, "sources.json")
	dist := filepath.Join(root, "dist")
	writeConfig := func(domainURL string) {
		t.Helper()
		cfg := config{Domains: []source{{Tag: "ai", URL: domainURL, Format: "mihomo"}}, IPs: []source{{Tag: "telegram", URL: base + "/ips"}}}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(base + "/domains")
	if err := build(context.Background(), configPath, dist, client); err != nil {
		t.Fatal(err)
	}
	var sites routercommon.GeoSiteList
	siteData, err := os.ReadFile(filepath.Join(dist, "geosite.dat"))
	if err != nil || proto.Unmarshal(siteData, &sites) != nil || len(sites.Entry) != 1 || sites.Entry[0].CountryCode != "AI" || len(sites.Entry[0].Domain) != 2 {
		t.Fatalf("geosite roundtrip: %v, %v", sites.Entry, err)
	}
	var ips routercommon.GeoIPList
	ipData, err := os.ReadFile(filepath.Join(dist, "geoip.dat"))
	if err != nil || proto.Unmarshal(ipData, &ips) != nil || len(ips.Entry) != 1 || ips.Entry[0].CountryCode != "TELEGRAM" || len(ips.Entry[0].Cidr) != 2 {
		t.Fatalf("geoip roundtrip: %v, %v", ips.Entry, err)
	}
	var m manifest
	manifestData, err := os.ReadFile(filepath.Join(dist, "manifest.json"))
	if err != nil || json.Unmarshal(manifestData, &m) != nil || len(m.Inputs) != 2 || m.Outputs[0].SHA256 != hash(siteData) || m.Outputs[1].SHA256 != hash(ipData) {
		t.Fatalf("manifest: %v, %v", m, err)
	}
	writeConfig(base + "/missing")
	if err := build(context.Background(), configPath, dist, client); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected source error, got %v", err)
	}
	stillThere, err := os.ReadFile(filepath.Join(dist, "geosite.dat"))
	if err != nil || !reflect.DeepEqual(stillThere, siteData) {
		t.Fatalf("failed build changed publication: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
