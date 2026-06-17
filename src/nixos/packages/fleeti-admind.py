#!/usr/bin/env python3
# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# fleeti-admind: the always-running Fleeti device agent.
#
# Responsibilities:
#   - When unpaired: register a stable pairing code with the Fleeti server and poll
#     until an administrator claims it, then store the issued device token.
#   - When paired: report telemetry (Fleeti system version, heartbeat, update status)
#     to the server on a fixed interval.
#   - Publish a world-readable status file for the Fleeti Admin "Provision" GUI page.
#
# It speaks only HTTP to the server and uses the Python standard library only.

import json
import os
import shlex
import signal
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request


AGENT_VERSION = "1.0.0"


def env(name, default=""):
    value = os.environ.get(name)
    if value is None:
        return default
    return value


def env_int(name, default):
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        return int(raw.strip())
    except ValueError:
        return default


def read_os_release_field(path, name):
    prefix = name + "="
    try:
        with open(path, encoding="utf-8") as os_release_file:
            for line in os_release_file:
                if not line.startswith(prefix):
                    continue

                raw_value = line[len(prefix):].strip()
                try:
                    parts = shlex.split(raw_value)
                except ValueError:
                    return None

                if len(parts) != 1:
                    return None

                value = parts[0].strip()
                return value or None
    except OSError:
        return None

    return None


def read_machine_id():
    for path in ("/etc/machine-id", "/var/lib/dbus/machine-id"):
        try:
            with open(path, encoding="utf-8") as handle:
                value = handle.read().strip()
                if value:
                    return value
        except OSError:
            continue

    return ""


def read_hostname():
    try:
        return socket.gethostname()
    except OSError:
        return ""


def read_serial():
    try:
        with open("/sys/class/dmi/id/product_serial", encoding="utf-8") as handle:
            value = handle.read().strip()
            # Common placeholder values reported by firmware are not useful.
            if value and value.lower() not in ("", "none", "to be filled by o.e.m.", "default string"):
                return value
    except OSError:
        pass

    return ""


def read_uptime_seconds():
    try:
        with open("/proc/uptime", encoding="utf-8") as handle:
            return int(float(handle.read().split()[0]))
    except (OSError, ValueError, IndexError):
        return 0


def parse_json(text):
    try:
        payload = json.loads(text)
    except (json.JSONDecodeError, TypeError):
        return None

    if not isinstance(payload, dict):
        return None

    return payload


def post_json(url, payload, token=None, timeout=15):
    data = json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token

    request = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read().decode("utf-8", "replace")
            return response.status, parse_json(body)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        return exc.code, parse_json(body)


class Agent:
    def __init__(self):
        self.fleet_id = env("FLEETI_ADMIND_FLEET_ID").strip()
        self.server_url = env("FLEETI_ADMIND_SERVER_URL").strip().rstrip("/")
        self.state_dir = env("FLEETI_ADMIND_STATE_DIR", "/var/lib/fleeti/admind")
        self.runtime_dir = env("FLEETI_ADMIND_RUNTIME_DIR", "/run/fleeti/admind")
        self.os_release = env("FLEETI_ADMIND_OS_RELEASE", "/etc/os-release")
        self.telemetry_interval = env_int("FLEETI_ADMIND_TELEMETRY_INTERVAL", 60)
        self.poll_interval = env_int("FLEETI_ADMIND_POLL_INTERVAL", 5)
        self.update_check_interval = env_int("FLEETI_ADMIND_UPDATE_CHECK_INTERVAL", 900)
        self.sysupdate = env("FLEETI_SYSTEMD_SYSUPDATE")

        self.machine_id = read_machine_id()
        self.state_path = os.path.join(self.state_dir, "state.json")
        self.status_path = os.path.join(self.runtime_dir, "status.json")

        self.state = {"paired": False, "device_id": "", "device_token": "", "code": ""}
        self.last_error = ""
        self.last_telemetry_at = ""
        self.update_status = {}
        self.last_update_check = 0.0

        self.stop = threading.Event()

    # --- identity ---

    def image_version(self):
        return read_os_release_field(self.os_release, "IMAGE_VERSION") or "unknown"

    # --- persistence ---

    def load_state(self):
        try:
            with open(self.state_path, encoding="utf-8") as handle:
                data = json.load(handle)
            if isinstance(data, dict):
                self.state.update(data)
        except (OSError, json.JSONDecodeError):
            pass

    def save_state(self):
        self._write_json_atomic(self.state_path, self.state, 0o600)

    def write_status(self, disabled=False):
        status = {
            "disabled": disabled,
            "paired": bool(self.state.get("paired")),
            "code": "" if self.state.get("paired") else self.state.get("code", ""),
            "device_id": self.state.get("device_id", ""),
            "hostname": read_hostname(),
            "fleet_id": self.fleet_id,
            "server_url": self.server_url,
            "image_version": self.image_version(),
            "agent_version": AGENT_VERSION,
            "last_telemetry_at": self.last_telemetry_at,
            "last_error": self.last_error,
        }
        self._write_json_atomic(self.status_path, status, 0o644)

    def _write_json_atomic(self, path, payload, mode):
        directory = os.path.dirname(path)
        try:
            os.makedirs(directory, exist_ok=True)
        except OSError:
            pass

        tmp = path + ".tmp"
        try:
            with open(tmp, "w", encoding="utf-8") as handle:
                json.dump(payload, handle)
            os.chmod(tmp, mode)
            os.replace(tmp, path)
        except OSError as exc:
            self.last_error = "failed to write %s: %s" % (path, exc)

    # --- server calls ---

    def api(self, path):
        return self.server_url + path

    def enroll_start(self):
        payload = {
            "fleet_id": self.fleet_id,
            "machine_id": self.machine_id,
            "hostname": read_hostname(),
            "serial": read_serial(),
            "version": self.image_version(),
            "agent_version": AGENT_VERSION,
        }
        try:
            status, body = post_json(self.api("/api/v1/device/enroll/start"), payload)
        except urllib.error.URLError as exc:
            self.last_error = "enroll start failed: %s" % exc
            return None

        if status == 200 and body and body.get("code"):
            self.last_error = ""
            return body["code"]

        self.last_error = "enroll start rejected (%s)" % status
        return None

    def enroll_poll(self, code):
        payload = {"code": code, "machine_id": self.machine_id}
        try:
            status, body = post_json(self.api("/api/v1/device/enroll/poll"), payload)
        except urllib.error.URLError as exc:
            self.last_error = "enroll poll failed: %s" % exc
            return "error", None

        if status == 404:
            return "notfound", None

        if status != 200 or body is None:
            self.last_error = "enroll poll rejected (%s)" % status
            return "error", None

        self.last_error = ""
        return body.get("status", "pending"), body

    def send_telemetry(self):
        payload = {
            "reported_version": self.image_version(),
            "agent_version": AGENT_VERSION,
            "update_state": "healthy",
            "uptime_seconds": read_uptime_seconds(),
            "current_version": self.image_version(),
        }
        payload.update(self.refresh_update_status())

        try:
            status, _ = post_json(
                self.api("/api/v1/device/telemetry"),
                payload,
                token=self.state.get("device_token"),
            )
        except urllib.error.URLError as exc:
            self.last_error = "telemetry failed: %s" % exc
            return

        if status == 401:
            # Token was revoked (device deleted / re-pair). Drop it and re-enroll.
            self.last_error = "device token rejected; re-enrolling"
            self.state = {"paired": False, "device_id": "", "device_token": "", "code": ""}
            self.save_state()
            return

        if status != 200:
            self.last_error = "telemetry rejected (%s)" % status
            return

        self.last_error = ""
        self.last_telemetry_at = time.strftime("%Y-%m-%d %H:%M:%S", time.gmtime())

    def refresh_update_status(self):
        now = time.monotonic()
        if self.update_status and (now - self.last_update_check) < self.update_check_interval:
            return self.update_status

        self.last_update_check = now
        self.update_status = self.compute_update_status()
        return self.update_status

    def compute_update_status(self):
        result = {}
        if not self.sysupdate:
            return result

        # The agent runs as root and can call systemd-sysupdate directly.
        try:
            pending = subprocess.run(
                [self.sysupdate, "--no-pager", "pending"],
                capture_output=True, text=True, timeout=30, check=False,
            )
            result["update_pending"] = pending.returncode == 0
        except (OSError, subprocess.SubprocessError):
            pass

        try:
            check = subprocess.run(
                [self.sysupdate, "--json=short", "--no-pager", "check-new"],
                capture_output=True, text=True, timeout=60, check=False,
            )
            if check.returncode == 0:
                data = parse_json(check.stdout)
                available = data.get("available") if data else None
                if isinstance(available, str) and available.strip():
                    result["available_version"] = available.strip()
                    result["desired_version"] = available.strip()
        except (OSError, subprocess.SubprocessError):
            pass

        return result

    # --- loops ---

    def do_enrollment(self):
        code = self.state.get("code", "")
        if not code:
            code = self.enroll_start()
            if code:
                self.state["code"] = code
                self.save_state()
        self.write_status()

        while not self.stop.is_set() and not self.state.get("paired"):
            if not code:
                self.write_status()
                self._sleep(self.poll_interval)
                code = self.enroll_start()
                if code:
                    self.state["code"] = code
                    self.save_state()
                    self.write_status()
                continue

            status, body = self.enroll_poll(code)
            if status == "claimed":
                token = body.get("device_token") if body else ""
                if token:
                    self.state = {
                        "paired": True,
                        "device_id": body.get("device_id", ""),
                        "device_token": token,
                        "code": "",
                    }
                    self.save_state()
                    self.write_status()
                    return
                # Claimed but no token was delivered to us: request a fresh code.
                code = ""
                self.state["code"] = ""
                self.save_state()
                continue

            if status in ("expired", "notfound"):
                code = ""
                self.state["code"] = ""
                self.save_state()
                continue

            self.write_status()
            self._sleep(self.poll_interval)

    def do_telemetry_cycle(self):
        self.send_telemetry()
        self.write_status()
        self._sleep(self.telemetry_interval)

    def run(self):
        signal.signal(signal.SIGTERM, self._handle_signal)
        signal.signal(signal.SIGINT, self._handle_signal)

        if not self.fleet_id or not self.server_url:
            self.last_error = "device management is not configured for this image"
            self.write_status(disabled=True)
            while not self.stop.is_set():
                self._sleep(60)
            return

        self.load_state()
        self.write_status()

        while not self.stop.is_set():
            if self.state.get("paired"):
                self.do_telemetry_cycle()
            else:
                self.do_enrollment()

    def _handle_signal(self, _signum, _frame):
        self.stop.set()

    def _sleep(self, seconds):
        # Sleep in short slices so SIGTERM (systemctl stop) is honored promptly.
        deadline = time.monotonic() + seconds
        while not self.stop.is_set():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return
            time.sleep(min(1.0, remaining))


def main(argv):
    command = argv[1] if len(argv) > 1 else "serve"
    agent = Agent()

    if command == "status":
        try:
            with open(agent.status_path, encoding="utf-8") as handle:
                print(handle.read())
        except OSError as exc:
            print("no status available: %s" % exc)
        return 0

    if command not in ("serve", ""):
        print("usage: fleeti-admind [serve|status]")
        return 2

    agent.run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
