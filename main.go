package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
)

const maxSourceBytes = 64 << 20

type source struct {
	Tag    string `json:"tag"`
	URL    string `json:"url"`
	Format string `json:"format,omitempty"`
}

type config struct {
	Domains []source `json:"domains"`
	IPs     []source `json:"ips"`
}

type inputRecord struct {
	Kind    string `json:"kind"`
	Tag     string `json:"tag"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Entries int    `json:"entries"`
}

type outputRecord struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type manifest struct {
	BuiltAt time.Time      `json:"built_at"`
	Inputs  []inputRecord  `json:"inputs"`
	Outputs []outputRecord `json:"outputs"`
}

type fetched struct {
	kind, tag string
	data      []byte
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := build(ctx, "sources.json", "dist", &http.Client{Timeout: 3 * time.Minute}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(ctx context.Context, configPath, dist string, client *http.Client) error {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg config
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("sources.json: %w", err)
	}
	if len(cfg.Domains)+len(cfg.IPs) == 0 {
		return errors.New("sources.json has no sources")
	}
	for _, group := range [][]source{cfg.Domains, cfg.IPs} {
		seenTags := map[string]bool{}
		for _, src := range group {
			if !validTag(src.Tag) || seenTags[strings.ToLower(src.Tag)] {
				return fmt.Errorf("invalid or duplicate tag %q", src.Tag)
			}
			seenTags[strings.ToLower(src.Tag)] = true
			u, err := url.Parse(src.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("invalid URL for %s: %q", src.Tag, src.URL)
			}
		}
	}
	for _, src := range cfg.Domains {
		if src.Format != "mihomo" && src.Format != "suffix" {
			return fmt.Errorf("unsupported domain format for %s: %q", src.Tag, src.Format)
		}
	}

	sites := &routercommon.GeoSiteList{}
	ips := &routercommon.GeoIPList{}
	files := []fetched{}
	records := []inputRecord{}
	for _, group := range []struct {
		kind string
		list []source
	}{{"domains", cfg.Domains}, {"ips", cfg.IPs}} {
		for _, src := range group.list {
			data, err := download(ctx, client, src.URL)
			if err != nil {
				return fmt.Errorf("%s %s: %w", group.kind, src.Tag, err)
			}
			var count int
			if group.kind == "domains" {
				values, err := parseDomains(data, src.Format)
				if err != nil {
					return fmt.Errorf("domains %s: %w", src.Tag, err)
				}
				count = len(values)
				sites.Entry = append(sites.Entry, &routercommon.GeoSite{CountryCode: strings.ToUpper(src.Tag), Domain: values})
			} else {
				values, err := parseIPs(data)
				if err != nil {
					return fmt.Errorf("ips %s: %w", src.Tag, err)
				}
				count = len(values)
				ips.Entry = append(ips.Entry, &routercommon.GeoIP{CountryCode: strings.ToUpper(src.Tag), Cidr: values})
			}
			files = append(files, fetched{group.kind, src.Tag, data})
			records = append(records, inputRecord{group.kind, src.Tag, src.URL, hash(data), count})
		}
	}
	marshal := proto.MarshalOptions{Deterministic: true}
	geosite, err := marshal.Marshal(sites)
	if err != nil {
		return err
	}
	geoip, err := marshal.Marshal(ips)
	if err != nil {
		return err
	}
	m := manifest{BuiltAt: time.Now().UTC(), Inputs: records, Outputs: []outputRecord{
		{"geosite.dat", hash(geosite), len(geosite)},
		{"geoip.dat", hash(geoip), len(geoip)},
	}}
	manifestData, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	// Build in a sibling directory so an invalid input never replaces a published build.
	staged, err := os.MkdirTemp(filepath.Dir(dist), ".happ-routing-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	if err := os.MkdirAll(filepath.Join(staged, "sources", "domains"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(staged, "sources", "ips"), 0755); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(staged, "sources", file.kind, file.tag+".txt"), file.data, 0644); err != nil {
			return err
		}
	}
	for name, data := range map[string][]byte{"geosite.dat": geosite, "geoip.dat": geoip, "manifest.json": append(manifestData, '\n')} {
		if err := os.WriteFile(filepath.Join(staged, name), data, 0644); err != nil {
			return err
		}
	}
	backup := staged + "-previous"
	if _, err := os.Stat(dist); err == nil {
		if err := os.Rename(dist, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, dist); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, dist)
		}
		return err
	}
	return os.RemoveAll(backup)
}

func download(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceBytes {
		return nil, errors.New("source exceeds 64 MiB")
	}
	return data, nil
}

func parseDomains(data []byte, format string) ([]*routercommon.Domain, error) {
	var domains []*routercommon.Domain
	seen := map[string]bool{}
	err := lines(data, func(line string) error {
		kind := routercommon.Domain_Full
		if format == "suffix" {
			kind = routercommon.Domain_RootDomain
		} else if strings.HasPrefix(line, "+.") {
			kind = routercommon.Domain_RootDomain
			line = strings.TrimPrefix(line, "+.")
		}
		if !isASCII(line) {
			ascii, err := idna.Lookup.ToASCII(line)
			if err != nil {
				return fmt.Errorf("invalid IDN %q: %w", line, err)
			}
			line = ascii
		}
		// Oversized names in upstream blocklists cannot match a valid DNS name.
		tooLong := len(line) > 253
		for _, label := range strings.Split(line, ".") {
			tooLong = tooLong || len(label) > 63
		}
		if tooLong {
			fmt.Fprintf(os.Stderr, "Skipping domain exceeding DNS length limits: %q\n", line)
			return nil
		}
		if !validDomain(line) {
			return fmt.Errorf("invalid domain %q", line)
		}
		line = strings.ToLower(line)
		key := fmt.Sprintf("%d:%s", kind, line)
		if !seen[key] {
			seen[key] = true
			domains = append(domains, &routercommon.Domain{Type: kind, Value: line})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, errors.New("empty domain source")
	}
	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Value == domains[j].Value {
			return domains[i].Type < domains[j].Type
		}
		return domains[i].Value < domains[j].Value
	})
	return domains, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return false
		}
	}
	return true
}

func parseIPs(data []byte) ([]*routercommon.CIDR, error) {
	var cidrs []*routercommon.CIDR
	seen := map[string]bool{}
	err := lines(data, func(line string) error {
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			addr, addrErr := netip.ParseAddr(line)
			if addrErr != nil {
				return fmt.Errorf("invalid IP/CIDR %q", line)
			}
			if addr.Zone() != "" {
				return fmt.Errorf("unsupported IP address: %q", line)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		if prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
			return fmt.Errorf("unsupported IP address: %q", line)
		}
		prefix = prefix.Masked()
		if !seen[prefix.String()] {
			seen[prefix.String()] = true
			cidrs = append(cidrs, &routercommon.CIDR{Ip: prefix.Addr().AsSlice(), Prefix: uint32(prefix.Bits())})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(cidrs) == 0 {
		return nil, errors.New("empty IP source")
	}
	sort.Slice(cidrs, func(i, j int) bool {
		if c := bytes.Compare(cidrs[i].Ip, cidrs[j].Ip); c != 0 {
			return c < 0
		}
		return cidrs[i].Prefix < cidrs[j].Prefix
	})
	return cidrs, nil
}

func lines(data []byte, consume func(string) error) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := consume(line); err != nil {
			return fmt.Errorf("line %d: %w", number, err)
		}
	}
	return scanner.Err()
}

func validTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, c := range tag {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func validDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
