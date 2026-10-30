#!/usr/bin/env python3
"""Prepare the Happ profile with stable references to the compiled policy."""

import base64
import json
from datetime import datetime
from pathlib import Path


def prepare():
    dist = Path("dist")
    manifest = json.loads((dist / "manifest.json").read_text())
    profile = json.loads(Path("happ-routing.json").read_text())
    outputs = {item["name"] for item in manifest["outputs"]}
    for action in ("direct", "proxy"):
        for kind, field, prefix in (("domains", "Sites", "geosite"), ("ips", "Ip", "geoip")):
            if f"mihomo/{action}-{kind}.txt" not in outputs:
                raise ValueError(f"Missing compiled policy: {action}-{kind}")
            profile[action.title() + field] = [f"{prefix}:routing-{action}"]
    profile["LastUpdated"] = str(int(datetime.fromisoformat(manifest["built_at"].replace("Z", "+00:00")).timestamp()))
    encoded = json.dumps(profile, ensure_ascii=False, separators=(",", ":")).encode()
    link = "happ://routing/add/" + base64.b64encode(encoded).decode()
    if json.loads(base64.b64decode(link.split("/", 4)[-1])) != profile:
        raise ValueError("Import-link round trip failed")
    (dist / "happ-routing.json").write_text(json.dumps(profile, ensure_ascii=False, indent=2) + "\n")
    (dist / "happ-import-link.txt").write_text(link + "\n")


if __name__ == "__main__":
    prepare()
