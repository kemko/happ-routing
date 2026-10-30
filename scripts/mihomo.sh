#!/usr/bin/env bash
set -euo pipefail

for action in direct proxy; do
  mihomo convert-ruleset domain text "dist/mihomo/$action-domains.txt" "dist/mihomo/$action-domains.mrs"
  mihomo convert-ruleset ipcidr text "dist/mihomo/$action-ips.txt" "dist/mihomo/$action-ips.mrs"
done
cp mihomo-rules.yaml dist/

# Validate the router fragment against local files before publishing it.
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
sed -e 's/type: http/type: file/' -e 's|./ruleset/happ-|./mihomo/|' mihomo-rules.yaml > "$test_dir/config.yaml"
cat >> "$test_dir/config.yaml" <<'EOF'
proxy-groups:
  - name: Proxy
    type: select
    proxies: [DIRECT]
EOF
mihomo -t -d "$PWD/dist" -f "$test_dir/config.yaml"
