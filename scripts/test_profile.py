import base64
import json
import os
from pathlib import Path
import tempfile
import unittest

from profile import prepare


class ProfileTest(unittest.TestCase):
    def setUp(self):
        self.previous = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        os.chdir(self.temp.name)
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(os.chdir, self.previous)
        Path("dist").mkdir()
        manifest = {"built_at": "2026-10-20T03:17:00Z", "outputs": [
            {"name": f"mihomo/{action}-{kind}.txt"}
            for action in ("direct", "proxy") for kind in ("domains", "ips")
        ]}
        Path("dist/manifest.json").write_text(json.dumps(manifest))
        self.profile = {
            "DirectSites": ["domain:alib.ru"],
            "ProxySites": ["geosite:youtube", "domain:tailscale.com"],
            "BlockSites": [], "DirectIp": ["geoip:private"],
            "ProxyIp": [], "BlockIp": [], "RouteOrder": "direct-proxy-block",
        }

    def write_profile(self):
        Path("happ-routing.json").write_text(json.dumps(self.profile))

    def test_import_link_uses_compiled_policy(self):
        self.write_profile()
        prepare()
        result = json.loads(Path("dist/happ-routing.json").read_text())
        link = Path("dist/happ-import-link.txt").read_text().strip()
        self.assertEqual(json.loads(base64.b64decode(link.removeprefix("happ://routing/add/"))), result)
        for action in ("Direct", "Proxy"):
            self.assertEqual(result[action + "Sites"], [f"geosite:routing-{action.lower()}"])
            self.assertEqual(result[action + "Ip"], [f"geoip:routing-{action.lower()}"])
        self.assertEqual(result["LastUpdated"], "1792466220")

    def test_missing_compiled_policy_is_not_published(self):
        path = Path("dist/manifest.json")
        manifest = json.loads(path.read_text())
        manifest["outputs"].pop()
        path.write_text(json.dumps(manifest))
        self.write_profile()
        with self.assertRaisesRegex(ValueError, "Missing compiled policy"):
            prepare()
        self.assertFalse(Path("dist/happ-import-link.txt").exists())


if __name__ == "__main__":
    unittest.main()
