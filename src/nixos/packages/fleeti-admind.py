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

import collections
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

# fleeti-update emits progress on stdout as newline-delimited JSON, each line prefixed
# with this marker. The update worker streams those lines to surface live progress.
PROGRESS_PREFIX = "@@PROGRESS@@ "


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


EFIVARS_DIR = "/sys/firmware/efi/efivars"
EFI_GLOBAL_GUID = "8be4df61-93ca-11d2-aa0d-00e098032b8c"


def read_efivar_flag(name):
    # EFI variables carry 4 attribute bytes before the value.
    path = os.path.join(EFIVARS_DIR, "%s-%s" % (name, EFI_GLOBAL_GUID))
    try:
        with open(path, "rb") as handle:
            data = handle.read()
    except OSError:
        return None

    if len(data) < 5:
        return None

    return data[4] != 0


def read_secure_boot():
    return bool(read_efivar_flag("SecureBoot"))


def read_setup_mode():
    return bool(read_efivar_flag("SetupMode"))


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


def get_json(url, token=None, timeout=15):
    headers = {}
    if token:
        headers["Authorization"] = "Bearer " + token

    request = urllib.request.Request(url, headers=headers, method="GET")
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
        self.command_poll_interval = env_int("FLEETI_ADMIND_COMMAND_POLL_INTERVAL", 15)
        self.update_check_interval = env_int("FLEETI_ADMIND_UPDATE_CHECK_INTERVAL", 900)
        self.sysupdate = env("FLEETI_SYSTEMD_SYSUPDATE")
        self.systemctl = env("FLEETI_SYSTEMCTL")
        self.fleeti_update = env("FLEETI_UPDATE")
        self.tpm_helper = env("FLEETI_TPM_HELPER")

        self.machine_id = read_machine_id()
        self.state_path = os.path.join(self.state_dir, "state.json")
        self.status_path = os.path.join(self.runtime_dir, "status.json")

        self.state = {"paired": False, "device_id": "", "device_token": "", "code": "", "attest_nonce": ""}
        self.last_error = ""
        self.last_telemetry_at = ""
        self.last_telemetry_monotonic = 0.0
        self.update_status = {}
        self.last_update_check = 0.0

        # Update execution runs in a background worker thread so the main loop keeps
        # cycling (telemetry, command poll, status writes) while an update is in flight.
        # update_info is shared with that thread and guarded by update_lock; it is the
        # single source of update status surfaced in both status.json and telemetry.
        self.update_lock = threading.Lock()
        self.update_info = {"state": "idle", "phase": "", "fraction": 0.0, "target": "", "error": ""}
        self.update_thread = None
        # Local (GUI-initiated) update requests are dropped here by `fleeti-admind
        # request-update` and picked up by the main loop.
        self.request_path = os.path.join(self.runtime_dir, "request.json")

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
        # Hold update_lock across the whole write: it snapshots update_info atomically
        # and serialises the two writers (main loop + update worker) so neither clobbers
        # the other's temp file.
        with self.update_lock:
            info = self.update_info
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
                "update_state": info["state"],
                "update_phase": info["phase"],
                "update_progress": int(round(info["fraction"] * 100)),
                "update_target_version": info["target"],
                "update_error": info["error"],
            }
            self._write_json_atomic(self.status_path, status, 0o644)

    def set_update_info(self, **fields):
        with self.update_lock:
            self.update_info.update(fields)

    def update_active(self):
        return self.update_thread is not None and self.update_thread.is_alive()

    def write_update_request(self, target):
        # Called by the `request-update` subcommand (run as root via sudo from the GUI);
        # the running daemon picks the file up in check_local_request().
        payload = {"kind": "update", "target_version": (target or "").strip()}
        self._write_json_atomic(self.request_path, payload, 0o644)

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

    # --- TPM remote attestation ---

    def run_tpm_helper(self, args):
        # Runs the fleeti-tpm helper and returns its parsed JSON output, or None if
        # the device has no usable TPM. Failures are non-fatal: attestation is
        # best-effort and the device simply stays unattested.
        if not self.tpm_helper:
            return None

        try:
            proc = subprocess.run(
                [self.tpm_helper] + list(args),
                capture_output=True, text=True, timeout=30, check=False,
            )
        except (OSError, subprocess.SubprocessError) as exc:
            self.last_error = "tpm helper failed: %s" % exc
            return None

        if proc.returncode != 0:
            self.last_error = "tpm helper failed: %s" % (proc.stderr.strip() or "unknown error")
            return None

        return parse_json(proc.stdout)

    def register_attestation(self):
        # Create the TPM attestation key and register its public part with the
        # server (trust-on-first-use), seeding the first challenge nonce.
        init = self.run_tpm_helper(["init"])
        if not init or not init.get("ak_public"):
            return

        try:
            status, body = post_json(
                self.api("/api/v1/device/attest/register"),
                {"ak_public": init["ak_public"]},
                token=self.state.get("device_token"),
            )
        except urllib.error.URLError as exc:
            self.last_error = "attestation register failed: %s" % exc
            return

        if status == 200 and body and body.get("attest_nonce"):
            self.state["attest_nonce"] = body["attest_nonce"]
            self.save_state()

    def build_attestation(self, secure_boot):
        # Produce a signed TPM quote over the boot-measurement PCRs for the current
        # challenge nonce. PCR 11 (the UKI measurement) is always quoted; PCR 7
        # (Secure Boot state) is added when Secure Boot is enabled.
        nonce = self.state.get("attest_nonce", "")
        if not nonce:
            return None

        pcrs = "7,11" if secure_boot else "11"
        quote = self.run_tpm_helper(["quote", "--nonce", nonce, "--pcrs", pcrs])
        if not quote or not quote.get("attest"):
            return None

        return quote

    def send_telemetry(self):
        # Register the attestation key the first time we are paired (or after a
        # re-pair clears our local nonce).
        if self.tpm_helper and not self.state.get("attest_nonce"):
            self.register_attestation()

        secure_boot = read_secure_boot()
        with self.update_lock:
            live_state = self.update_info["state"]
        # Surface in-progress updates to the server dashboard. Idle maps to the existing
        # "healthy" default; reboot-required is reported as rebooting (update applied,
        # awaiting reboot).
        telemetry_state = {
            "idle": "healthy",
            "reboot-required": "rebooting",
        }.get(live_state, live_state)

        payload = {
            "reported_version": self.image_version(),
            "agent_version": AGENT_VERSION,
            "update_state": telemetry_state,
            "uptime_seconds": read_uptime_seconds(),
            "current_version": self.image_version(),
            "secure_boot": secure_boot,
            "setup_mode": read_setup_mode(),
        }
        payload.update(self.refresh_update_status())

        attestation = self.build_attestation(secure_boot)
        if attestation:
            payload["attestation"] = attestation

        try:
            status, body = post_json(
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
            self.state = {"paired": False, "device_id": "", "device_token": "", "code": "", "attest_nonce": ""}
            self.save_state()
            return

        if status != 200:
            self.last_error = "telemetry rejected (%s)" % status
            return

        # Adopt the next rolling challenge nonce for the following quote.
        if body and body.get("attest_nonce"):
            self.state["attest_nonce"] = body["attest_nonce"]
            self.save_state()

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
            # Local update requests (GUI "Install") work even before pairing.
            self.check_local_request()

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
                        "attest_nonce": "",
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

    def do_paired_cycle(self):
        self.check_local_request()

        now = time.monotonic()
        if self.last_telemetry_monotonic == 0.0 or (now - self.last_telemetry_monotonic) >= self.telemetry_interval:
            self.send_telemetry()
            self.last_telemetry_monotonic = time.monotonic()
            if not self.state.get("paired"):
                self.write_status()
                return

        self.poll_and_execute_commands()
        self.write_status()
        # While an update is in flight, cycle quickly so status.json stays fresh even
        # between the worker's own progress writes; otherwise poll at the normal cadence.
        self._sleep(1 if self.update_active() else self.command_poll_interval)

    def poll_and_execute_commands(self):
        if not self.state.get("paired"):
            return

        for command in self.get_commands():
            if self.stop.is_set():
                return

            self.execute_command(command)

    def get_commands(self):
        try:
            status, body = get_json(self.api("/api/v1/device/commands"), self.state.get("device_token"))
        except urllib.error.URLError as exc:
            self.last_error = "command poll failed: %s" % exc
            return []

        if status == 401:
            # Token revoked (device deleted / re-pair): drop it and re-enroll.
            self.last_error = "device token rejected; re-enrolling"
            self.state = {"paired": False, "device_id": "", "device_token": "", "code": ""}
            self.save_state()
            return []

        if status != 200 or not body:
            return []

        commands = body.get("commands")
        return commands if isinstance(commands, list) else []

    def execute_command(self, command):
        command_id = command.get("id")
        kind = command.get("kind")
        target = command.get("target_version", "")
        if not command_id or not kind:
            return

        # Acknowledge first so the command leaves the pending state and is not
        # picked up again on the next poll.
        self.report_command(command_id, "acknowledged", "")

        if kind == "update":
            # Run asynchronously so the main loop keeps cycling (and publishing live
            # progress) while the update runs. The worker reports the command result
            # and reboots on success.
            self._start_update(command_id, target, reboot_when_done=True)
        elif kind == "reboot":
            self.report_command(command_id, "succeeded", "Rebooting.")
            self.reboot()
        else:
            self.report_command(command_id, "failed", "Unknown command kind: %s" % kind)

    # --- update execution ---

    def check_local_request(self):
        # Pick up a local (GUI-initiated) update request, if any.
        if not os.path.exists(self.request_path):
            return

        try:
            with open(self.request_path, encoding="utf-8") as handle:
                raw = handle.read()
        except OSError:
            return

        # Consume the file before parsing so a malformed request isn't retried forever.
        try:
            os.remove(self.request_path)
        except OSError:
            pass

        payload = parse_json(raw)
        if not payload or payload.get("kind") != "update":
            return

        target = payload.get("target_version", "")
        if not isinstance(target, str):
            target = ""
        # Local installs don't auto-reboot; the GUI offers a Reboot button afterwards.
        self._start_update(None, target.strip(), reboot_when_done=False)

    def _start_update(self, command_id, target, reboot_when_done):
        if self.update_active():
            if command_id:
                self.report_command(command_id, "failed", "another update is already in progress")
            return

        with self.update_lock:
            self.update_info = {
                "state": "downloading",
                "phase": "Preparing",
                "fraction": 0.0,
                "target": target or "",
                "error": "",
            }
        self.write_status()

        self.update_thread = threading.Thread(
            target=self._update_worker,
            args=(command_id, target, reboot_when_done),
            daemon=True,
        )
        self.update_thread.start()

    def _update_worker(self, command_id, target, reboot_when_done):
        returncode, output = self.run_update(target)

        if returncode != 0:
            self.set_update_info(state="failed", phase="Update failed", error=output or "update failed")
            self.write_status()
            if command_id:
                self.report_command(command_id, "failed", output)
            return

        if reboot_when_done:
            self.set_update_info(state="rebooting", phase="Rebooting", fraction=1.0)
            self.write_status()
            if command_id:
                self.report_command(command_id, "succeeded", "Update installed; rebooting.")
            self.reboot()
        else:
            self.set_update_info(state="reboot-required", phase="Update installed", fraction=1.0)
            self.write_status()
            if command_id:
                self.report_command(command_id, "succeeded", "Update installed; reboot to apply.")

    def run_update(self, target):
        # Prefer the delta updater: it reconstructs the target image from chunks
        # already on the device plus the few changed chunks, then hands off to
        # systemd-sysupdate to apply locally. On any failure fall back to the
        # full-image download path so updates still complete.
        if self.fleeti_update:
            returncode, output = self.run_delta_update(target)
            if returncode == 0:
                return 0, ""
            self.last_error = "delta update failed; falling back to full download: %s" % output
            # The full path has no progress stream; reflect that we're applying.
            self.set_update_info(state="applying", phase="Downloading full image", fraction=0.0)
            self.write_status()

        return self.run_full_update(target)

    def run_delta_update(self, target):
        args = [self.fleeti_update]
        if target:
            args.append(target)

        returncode, output = self._run_streaming(args, timeout=3600, parse_progress=True)
        if returncode != 0:
            return returncode, (output.strip() or "delta update failed")
        return 0, ""

    def run_full_update(self, target):
        if not self.sysupdate:
            return 1, "systemd-sysupdate is not configured"

        args = [self.sysupdate, "--no-pager", "update"]
        if target:
            args.append(target)

        returncode, output = self._run_streaming(args, timeout=1800, parse_progress=False)
        if returncode != 0:
            return returncode, (output.strip() or "update failed")
        return 0, ""

    def _run_streaming(self, args, timeout, parse_progress=False):
        # Run a subprocess, streaming its merged stdout/stderr line by line so the
        # update worker can surface progress live. Returns (returncode, tail) where tail
        # is the most recent output lines, used for error reporting. A watchdog timer
        # kills the process if it overruns the timeout (the line iterator blocks on
        # output, so the deadline can't be enforced inline).
        try:
            proc = subprocess.Popen(
                args,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
            )
        except (OSError, ValueError) as exc:
            return 1, "failed to run %s: %s" % (args[0], exc)

        tail = collections.deque(maxlen=50)
        timed_out = {"hit": False}

        def on_timeout():
            timed_out["hit"] = True
            proc.kill()

        timer = threading.Timer(timeout, on_timeout)
        timer.start()
        try:
            for line in proc.stdout:
                line = line.rstrip("\n")
                if parse_progress and line.startswith(PROGRESS_PREFIX):
                    self._apply_progress_line(line)
                elif line:
                    tail.append(line)
                if self.stop.is_set():
                    proc.kill()
                    break
        finally:
            timer.cancel()
            try:
                proc.stdout.close()
            except OSError:
                pass

        returncode = proc.wait()
        if timed_out["hit"]:
            tail.append("timed out after %ds" % timeout)
        return returncode, "\n".join(tail)

    def _apply_progress_line(self, line):
        payload = parse_json(line[len(PROGRESS_PREFIX):])
        if not payload:
            return

        fields = {}
        state = payload.get("state")
        if isinstance(state, str) and state:
            fields["state"] = state
        phase = payload.get("phase")
        if isinstance(phase, str):
            fields["phase"] = phase
        fraction = payload.get("fraction")
        if isinstance(fraction, (int, float)):
            fields["fraction"] = max(0.0, min(1.0, float(fraction)))

        if not fields:
            return

        with self.update_lock:
            self.update_info.update(fields)
        self.write_status()

    def reboot(self):
        if not self.systemctl:
            self.last_error = "systemctl is not configured"
            return

        try:
            subprocess.run([self.systemctl, "reboot"], capture_output=True, text=True, timeout=60, check=False)
        except (OSError, subprocess.SubprocessError) as exc:
            self.last_error = "reboot failed: %s" % exc

    def report_command(self, command_id, status, result):
        payload = {"status": status, "result": result}
        try:
            post_json(
                self.api("/api/v1/device/commands/%s/result" % command_id),
                payload,
                token=self.state.get("device_token"),
            )
        except urllib.error.URLError as exc:
            self.last_error = "command result failed: %s" % exc

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
                self.do_paired_cycle()
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

    if command == "request-update":
        # Queue a local update for the running daemon to perform (used by the GUI via
        # sudo). An optional target version may be given; empty means latest.
        target = argv[2] if len(argv) > 2 else ""
        agent.write_update_request(target)
        return 0

    if command not in ("serve", ""):
        print("usage: fleeti-admind [serve|status|request-update [version]]")
        return 2

    agent.run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
