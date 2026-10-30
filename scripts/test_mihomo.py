"""Exercise the published rule order and MRS matching with a real Mihomo core."""

import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import unittest
from urllib.request import ProxyHandler, build_opener


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()

    def log_message(self, *_):
        pass


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@unittest.skipUnless(shutil.which("mihomo"), "mihomo is not on PATH")
class MihomoTest(unittest.TestCase):
    def test_mrs_priority_exact_suffix_and_dns_fallback(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entries = {
                "direct-domains": "+.both.test\n",
                "proxy-domains": "+.both.test\n+.proxy.test\nexact.test\n",
                "direct-ips": "127.0.0.1/32\n",
                "proxy-ips": "127.0.0.2/32\n",
            }
            providers = {}
            for name, text in entries.items():
                source, target = root / f"{name}.txt", root / f"{name}.mrs"
                source.write_text(text)
                behavior = "domain" if name.endswith("domains") else "ipcidr"
                subprocess.run(["mihomo", "convert-ruleset", behavior, "text", str(source), str(target)], check=True, capture_output=True)
                providers[name] = {"type": "file", "behavior": behavior, "format": "mrs", "path": str(target)}

            fragment = (Path(__file__).resolve().parent.parent / "mihomo-rules.yaml").read_text()
            rules = [line.strip()[2:] for line in fragment.split("\nrules:\n", 1)[1].splitlines() if line.strip()]
            port, controller = free_port(), free_port()
            config = {
                "mode": "rule", "mixed-port": port, "external-controller": f"127.0.0.1:{controller}",
                "log-level": "debug", "ipv6": False,
                "proxy-groups": [{"name": "Proxy", "type": "select", "proxies": ["REJECT"]}],
                "rule-providers": providers, "rules": rules,
                "hosts": {name: "127.0.0.1" for name in ("both.test", "sub.both.test", "proxy.test", "sub.proxy.test", "exact.test", "sub.exact.test", "unmatched.test")},
                "dns": {"enable": True, "ipv6": False, "use-hosts": True, "nameserver": ["127.0.0.1"]},
            }
            config["hosts"]["ip-fallback.test"] = "127.0.0.2"
            path = root / "config.json"
            path.write_text(json.dumps(config))
            server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            try:
                with (root / "mihomo.log").open("w+") as log:
                    process = subprocess.Popen(["mihomo", "-d", tmp, "-f", str(path)], stdout=log, stderr=log)
                    try:
                        opener = build_opener(ProxyHandler({}))
                        for _ in range(100):
                            try:
                                with opener.open(f"http://127.0.0.1:{controller}/rules", timeout=0.2):
                                    # The controller starts before the proxy listener.
                                    with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                                        break
                            except OSError:
                                if process.poll() is not None:
                                    break
                                time.sleep(0.05)
                        else:
                            self.fail("Mihomo API/proxy did not become ready:\n" + (root / "mihomo.log").read_text())
                        self.assertIsNone(process.poll())
                        for name, direct in (
                            ("both.test", True), ("sub.both.test", True),
                            ("proxy.test", False), ("sub.proxy.test", False),
                            ("exact.test", False), ("sub.exact.test", True),
                            ("unmatched.test", True), ("ip-fallback.test", False),
                            ("127.0.0.1", True), ("127.0.0.2", False),
                        ):
                            with self.subTest(destination=name):
                                connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
                                try:
                                    connection.request("GET", f"http://{name}:{server.server_port}/")
                                    response = connection.getresponse()
                                    self.assertEqual(response.status == 200, direct, f"{name}: HTTP {response.status}")
                                    response.read()
                                finally:
                                    connection.close()
                    finally:
                        process.terminate()
                        try:
                            process.wait(timeout=5)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait()
                        log.seek(0)
                        output = log.read()
                        if process.returncode not in (0, -15):
                            self.fail(output)
            finally:
                server.shutdown()
                server.server_close()


if __name__ == "__main__":
    unittest.main()
