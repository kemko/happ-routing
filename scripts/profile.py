#!/usr/bin/env python3
"""Validate the profile against the built categories and prepare its import link."""

import base64
import json
from datetime import datetime
from pathlib import Path


def prepare():
    dist = Path("dist")
    manifest = json.loads((dist / "manifest.json").read_text())
    profile = json.loads(Path("happ-routing.json").read_text())
    categories = {
        (item["kind"], item["tag"].lower()) for item in manifest["inputs"]
    }
    for field in ("DirectSites", "ProxySites", "BlockSites"):
        for rule in profile[field]:
            if rule.startswith("geosite:"):
                if ("domains", rule[8:].lower()) not in categories:
                    raise ValueError(f"Missing GeoSite category: {rule}")
    for field in ("DirectIp", "ProxyIp", "BlockIp"):
        for rule in profile[field]:
            if rule.startswith("geoip:"):
                if ("ips", rule[6:].lower()) not in categories:
                    raise ValueError(f"Missing GeoIP category: {rule}")
    if profile["RouteOrder"].split("-").index("direct") > profile["RouteOrder"].split("-").index("proxy"):
        raise ValueError("Direct exceptions must precede proxy rules")
    profile["LastUpdated"] = str(int(datetime.fromisoformat(manifest["built_at"].replace("Z", "+00:00")).timestamp()))
    encoded = json.dumps(profile, ensure_ascii=False, separators=(",", ":")).encode()
    link = "happ://routing/add/" + base64.b64encode(encoded).decode()
    if json.loads(base64.b64decode(link.split("/", 4)[-1])) != profile:
        raise ValueError("Import-link round trip failed")
    (dist / "happ-routing.json").write_text(json.dumps(profile, ensure_ascii=False, indent=2) + "\n")
    (dist / "happ-import-link.txt").write_text(link + "\n")


if __name__ == "__main__":
    prepare()
