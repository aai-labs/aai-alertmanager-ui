#!/usr/bin/env python3
"""Exercise the UI backend against real, isolated Alertmanager containers.

Requires Docker, Python 3.9+, and a built UI server. No cluster access is used.
"""
import argparse
import datetime as dt
import json
from html.parser import HTMLParser
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import urllib.parse
import uuid


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def request(base, path, method="GET", body=None, origin=None, expected=200):
    data = None if body is None else json.dumps(body).encode()
    headers = {"Content-Type": "application/json"}
    if origin:
        headers["Origin"] = origin
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    if status != expected:
        raise AssertionError(f"{method} {path}: expected {expected}, got {status}: {raw[:500]!r}")
    if not raw:
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw.decode()


class AssetParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.assets = []
        self.base = None

    def handle_starttag(self, tag, attributes):
        attrs = dict(attributes)
        if tag == "base":
            self.base = attrs.get("href")
        if tag == "script" and attrs.get("src"):
            self.assets.append(attrs["src"])
        if tag == "link" and attrs.get("rel") == "stylesheet":
            self.assets.append(attrs["href"])


def check_embedded_ui(origin, base_path):
    for path in ("/", "/bookmarked-view"):
        document = request(origin, base_path + path)
        assert isinstance(document, str) and '<div id="root">' in document, "Build and embed the frontend before running compatibility tests"
        parsed = AssetParser()
        parsed.feed(document)
        assert parsed.base == base_path + "/", f"Incorrect injected base: {parsed.base}"
        assert any(asset.endswith(".js") for asset in parsed.assets), "Missing built JavaScript bundle"
        for asset in parsed.assets:
            url = urllib.parse.urljoin(origin + parsed.base, asset)
            assert url.startswith(origin + base_path + "/"), f"Asset escapes base path: {url}"
            payload = request(origin, url[len(origin):])
            assert isinstance(payload, str) and payload and "<html" not in payload[:100].lower(), f"Invalid asset: {url}"


def eventually(check, label, timeout=30):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (OSError, AssertionError, ValueError) as error:
            last = error
        time.sleep(0.2)
    raise AssertionError(f"Timed out waiting for {label}; last error: {last}")


def timestamp(offset):
    return (dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=offset)).isoformat().replace("+00:00", "Z")


def run_version(binary, version, base_path):
    name = "alertmanager-ui-compat-" + uuid.uuid4().hex[:12]
    process = None
    with tempfile.TemporaryDirectory(prefix="alertmanager-ui-compat-") as temp:
        config = Path(temp) / "alertmanager.yml"
        config.write_text("route:\n  receiver: discard\nreceivers:\n  - name: discard\n")
        log_path = Path(temp) / "server.log"
        try:
            command("docker", "create", "--name", name, "-p", "127.0.0.1::9093",
                    f"quay.io/prometheus/alertmanager:v{version}",
                    "--config.file=/etc/alertmanager/test.yml", "--storage.path=/alertmanager",
                    "--cluster.listen-address=")
            command("docker", "cp", str(config), f"{name}:/etc/alertmanager/test.yml")
            command("docker", "start", name)
            published = command("docker", "port", name, "9093/tcp").splitlines()[0]
            upstream = "http://" + published
            eventually(lambda: request(upstream, "/-/ready"), "Alertmanager readiness")
            version_info = request(upstream, "/api/v2/status")["versionInfo"]
            assert version_info["version"] == version, version_info

            port = free_port()
            origin = f"http://127.0.0.1:{port}"
            base = origin + base_path
            env = {key: value for key, value in os.environ.items() if key not in (
                "CONFIG_FILE", "ALERTMANAGER_URL", "PROMETHEUS_URL", "READ_ONLY", "DEMO", "BASE_PATH", "LISTEN_ADDR")}
            env.update(ALERTMANAGER_URL=upstream, LISTEN_ADDR=f"127.0.0.1:{port}", BASE_PATH=base_path)
            with log_path.open("w") as logs:
                process = subprocess.Popen([str(binary)], env=env, stdout=logs, stderr=logs)
                eventually(lambda: request(base, "/api/context"), "UI readiness")
                check_embedded_ui(origin, base_path)
                request(upstream, "/api/v2/alerts", "POST", [{
                    "labels": {"alertname": "CompatibilitySmoke", "namespace": "example", "severity": "warning"},
                    "annotations": {"summary": "Synthetic compatibility test"},
                    "startsAt": timestamp(-60), "endsAt": timestamp(3600),
                }])
                alerts = eventually(lambda: request(base, "/api/alerts"), "forwarded alert")
                assert len(alerts) == 1 and alerts[0]["labels"]["alertname"] == "CompatibilitySmoke", alerts
                assert alerts[0]["status"]["state"] == "active", alerts

                silence = {
                    "matchers": [{"name": "alertname", "value": "CompatibilitySmoke", "isRegex": False, "isEqual": True},
                                 {"name": "namespace", "value": "example", "isRegex": False, "isEqual": True}],
                    "startsAt": timestamp(-10), "endsAt": timestamp(3600),
                    "createdBy": "compatibility-test", "comment": "Synthetic integration test",
                }
                preview = request(base, "/api/silences/preview", "POST", {"matchers": silence["matchers"]}, origin)
                assert preview["count"] == 1 and len(preview["alerts"]) == 1, preview
                regex = [{"name": "alertname", "value": "Compatibility.*", "isRegex": True, "isEqual": True}]
                assert request(base, "/api/silences/preview", "POST", {"matchers": regex}, origin)["count"] == 1
                regex[0]["value"] = "Compatibility"
                assert request(base, "/api/silences/preview", "POST", {"matchers": regex}, origin)["count"] == 0
                regex[0]["value"] = "(unclosed"
                request(base, "/api/silences/preview", "POST", {"matchers": regex}, origin, expected=400)
                created = request(base, "/api/silences", "POST", silence, origin)
                silence_id = created["silenceID"]
                eventually(lambda: any(s["id"] == silence_id for s in request(base, "/api/silences")), "silence listing")
                eventually(lambda: any(a["status"]["state"] == "suppressed" for a in request(base, "/api/alerts")), "silence suppression")
                silence.update(id=silence_id, comment="Updated synthetic integration test")
                updated = request(base, "/api/silences", "POST", silence, origin)
                silence_id = updated["silenceID"]
                silence["id"] = silence_id
                eventually(lambda: any(s["id"] == silence_id and s["comment"] == silence["comment"]
                                      for s in request(base, "/api/silences")), "edited silence")
                request(base, "/api/silences/" + silence_id, "DELETE", origin=origin)
                eventually(lambda: any(s["id"] == silence_id and s["status"]["state"] == "expired"
                                      for s in request(base, "/api/silences")), "expired silence")
                eventually(lambda: any(a["status"]["state"] == "active" for a in request(base, "/api/alerts")), "active alert after expiration")
                request(base, "/api/silences", "POST", silence, "https://untrusted.example", expected=403)
                process.terminate()
                process.wait(timeout=10)
                process = None
                env["READ_ONLY"] = "true"
                process = subprocess.Popen([str(binary)], env=env, stdout=logs, stderr=logs)
                eventually(lambda: request(base, "/api/context").get("readOnly"), "read-only readiness")
                request(base, "/api/silences", "POST", silence, origin, expected=403)
                request(base, "/api/silences/" + silence_id, "DELETE", origin=origin, expected=403)
                print(f"PASS Alertmanager {version}: alerts, matcher preview, create/edit/expire silence, suppression, CSRF, read-only, embedded assets, base path {base_path or '/'}", flush=True)
        except Exception:
            if log_path.exists():
                print(log_path.read_text())
            try:
                print(command("docker", "logs", name))
            except subprocess.CalledProcessError:
                pass
            raise
        finally:
            if process is not None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default=".build/alertmanager-ui")
    parser.add_argument("--versions", nargs="+", default=["0.27.0", "0.34.1"])
    args = parser.parse_args()
    binary = Path(args.binary).resolve()
    if not binary.is_file():
        parser.error(f"Build the UI server first; missing {binary}")
    for index, version in enumerate(args.versions):
        if not all(part.isdigit() for part in version.split(".")) or len(version.split(".")) != 3:
            parser.error("Versions must use numeric major.minor.patch format")
        run_version(binary, version, "" if index % 2 == 0 else "/attention")


if __name__ == "__main__":
    main()
