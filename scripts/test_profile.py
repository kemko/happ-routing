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
        manifest = {"built_at": "2026-10-20T03:17:00Z", "inputs": [
            {"kind": "domains", "tag": "youtube"},
            {"kind": "ips", "tag": "private"},
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

    def test_import_link_preserves_rules(self):
        self.write_profile()
        prepare()
        result = json.loads(Path("dist/happ-routing.json").read_text())
        link = Path("dist/happ-import-link.txt").read_text().strip()
        self.assertEqual(json.loads(base64.b64decode(link.removeprefix("happ://routing/add/"))), result)
        self.assertEqual(result["ProxySites"], self.profile["ProxySites"])
        self.assertEqual(result["DirectSites"], ["domain:alib.ru"])
        self.assertEqual(result["LastUpdated"], "1792466220")

    def test_missing_category_is_not_published(self):
        self.profile["ProxySites"].append("geosite:missing")
        self.write_profile()
        with self.assertRaisesRegex(ValueError, "Missing GeoSite category"):
            prepare()
        self.assertFalse(Path("dist/happ-import-link.txt").exists())


if __name__ == "__main__":
    unittest.main()
