#!/usr/bin/env python3
# pyright: reportUnknownVariableType=false, reportUnknownArgumentType=false, reportUnknownMemberType=false, reportMissingImports=false

from __future__ import annotations

import argparse
import json
import os
import posixpath
import shlex
import signal
import socket
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from pathlib import PurePosixPath
from typing import Any


class ConfigError(Exception):
    pass


def env_or_default(name: str, fallback: str) -> str:
    value = os.environ.get(name, "").strip()
    if value:
        return value
    return fallback


STATE_DIR = Path(env_or_default("MOLTHOUSE_STATE_DIR", "/var/lib/fleeti/molthouse"))
RUNTIME_DIR = Path(env_or_default("MOLTHOUSE_RUNTIME_DIR", "/run/fleeti/molthouse"))
CONFIG_PATH = STATE_DIR / "config.json"
STATE_PATH = RUNTIME_DIR / "state.json"
LAUNCH_PLAN_PATH = RUNTIME_DIR / "launch-plan.json"
VM_RUNTIME_PATH = RUNTIME_DIR / "vm-runtime.json"
BOOT_SHARES_PATH = RUNTIME_DIR / "boot-shares.json"
PID_PATH = RUNTIME_DIR / "molthoused.pid"
LOG_PATH = STATE_DIR / "molthoused.log"

BUS_NAME = env_or_default("MOLTHOUSE_DBUS_NAME", "ae.fleeti.MoltHouse1")
OBJECT_PATH = env_or_default("MOLTHOUSE_DBUS_OBJECT_PATH", "/ae/fleeti/MoltHouse1")
INTERFACE_NAME = env_or_default("MOLTHOUSE_DBUS_INTERFACE", "ae.fleeti.MoltHouse1")
BUS_KIND = env_or_default("MOLTHOUSE_DBUS_BUS", "system").lower()
VM_BACKEND = env_or_default("MOLTHOUSE_VM_BACKEND", "qemu").lower()
SYSTEMCTL_BIN = env_or_default("MOLTHOUSE_SYSTEMCTL", "systemctl")
SYSTEMD_VM_SERVICE = env_or_default("MOLTHOUSE_VM_SYSTEMD_SERVICE", "").strip()
HOST_SHARE_HOME = env_or_default("MOLTHOUSE_HOST_HOME", "/home/fleeti")
GUEST_SHARE_HOME = env_or_default("MOLTHOUSE_GUEST_HOME", "/home/fleeti")
GUEST_MOUNT_PORT = int(env_or_default("MOLTHOUSE_GUEST_MOUNT_PORT", "10770"))
GUEST_VSOCK_CID = int(env_or_default("MOLTHOUSE_GUEST_VSOCK_CID", "3"))
MICROVM_STATE_DIR = Path(env_or_default("MOLTHOUSE_MICROVM_STATE_DIR", "/var/lib/microvms"))
VIRTIOFSD_SOCKET_GROUP = env_or_default("MOLTHOUSE_VIRTIOFSD_SOCKET_GROUP", "kvm")
QMP_DEVICE_PREFIX = "molthouse-share-"
QMP_CHARDEV_PREFIX = "molthouse-char-"

INTROSPECTION_XML = f"""
<node>
  <interface name=\"{INTERFACE_NAME}\">
    <method name=\"GetState\">
      <arg name=\"state_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"StartVm\">
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"StopVm\">
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"RestartVm\">
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"ListShares\">
      <arg name=\"shares_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"AddShare\">
      <arg name=\"source\" type=\"s\" direction=\"in\"/>
      <arg name=\"mount_point\" type=\"s\" direction=\"in\"/>
      <arg name=\"read_only\" type=\"b\" direction=\"in\"/>
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"UpdateShare\">
      <arg name=\"share_id\" type=\"s\" direction=\"in\"/>
      <arg name=\"source\" type=\"s\" direction=\"in\"/>
      <arg name=\"mount_point\" type=\"s\" direction=\"in\"/>
      <arg name=\"read_only\" type=\"b\" direction=\"in\"/>
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"RemoveShare\">
      <arg name=\"share_id\" type=\"s\" direction=\"in\"/>
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"GetRecentLogs\">
      <arg name=\"lines\" type=\"i\" direction=\"in\"/>
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <method name=\"GetConsoleState\">
      <arg name=\"result_json\" type=\"s\" direction=\"out\"/>
    </method>
    <signal name=\"StateChanged\">
      <arg name=\"state_json\" type=\"s\"/>
    </signal>
    <signal name=\"SharesChanged\">
      <arg name=\"shares_json\" type=\"s\"/>
    </signal>
    <signal name=\"VmFailed\">
      <arg name=\"failure_json\" type=\"s\"/>
    </signal>
  </interface>
</node>
"""


def now_iso() -> str:
    return (
        datetime.now(timezone.utc)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z")
    )


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_path = path.with_suffix(path.suffix + ".tmp")
    temp_path.write_text(
        json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    temp_path.replace(path)


def ensure_runtime_dirs() -> None:
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    RUNTIME_DIR.mkdir(parents=True, exist_ok=True)


def default_config() -> dict[str, Any]:
    return {
        "version": 1,
        "runtime_support": {
            "assets_dir": env_or_default("MOLTHOUSE_ASSETS_DIR", ""),
            "qemu_binary": env_or_default("MOLTHOUSE_QEMU", "qemu-system-x86_64"),
            "virtiofsd_binary": env_or_default("MOLTHOUSE_VIRTIOFSD", "virtiofsd"),
        },
        "vm": {
            "name": "openclaw",
            "boot_vcpus": 1,
            "max_vcpus": 1,
            "memory_mib": 2049,
        },
        "guest": {
            "kernel": None,
            "initramfs": None,
            "disk": None,
            "cmdline": "console=ttyS0 console=hvc0",
        },
        "runtime": {
            "qmp_socket_path": env_or_default(
                "MOLTHOUSE_QMP_SOCKET", str(RUNTIME_DIR / "qmp.sock")
            ),
            "console_socket_path": str(RUNTIME_DIR / "console.sock"),
            "guest_mount_port": GUEST_MOUNT_PORT,
            "guest_vsock_cid": GUEST_VSOCK_CID,
            "writable_disk_path": str(STATE_DIR / "openclaw-runtime.img"),
            "writable_disk_size_mib": 4096,
        },
        "shares": [],
    }


def require_dict(parent: dict[str, Any], key: str) -> dict[str, Any]:
    value = parent.get(key)
    if not isinstance(value, dict):
        raise ConfigError(f"{key} must be an object")
    return value


def require_string(parent: dict[str, Any], key: str) -> str:
    value = parent.get(key)
    if not isinstance(value, str):
        raise ConfigError(f"{key} must be a string")
    value = value.strip()
    if value == "":
        raise ConfigError(f"{key} must not be empty")
    return value


def optional_string(parent: dict[str, Any], key: str) -> str | None:
    value = parent.get(key)
    if value is None:
        return None
    if not isinstance(value, str):
        raise ConfigError(f"{key} must be a string or null")
    value = value.strip()
    if value == "":
        return None
    return value


def require_positive_int(parent: dict[str, Any], key: str) -> int:
    value = parent.get(key)
    if not isinstance(value, int) or value < 1:
        raise ConfigError(f"{key} must be a positive integer")
    return value


def optional_bool(parent: dict[str, Any], key: str, default: bool) -> bool:
    value = parent.get(key)
    if value is None:
        return default
    if not isinstance(value, bool):
        raise ConfigError(f"{key} must be a boolean")
    return value


def expand_host_share_source(source: str, host_home: Path) -> Path:
    if source == "~":
        return host_home
    if source.startswith("~/"):
        return host_home / source[2:]
    return Path(source)


def read_host_share_home() -> Path:
    host_home = Path(HOST_SHARE_HOME)
    if not host_home.is_absolute():
        raise ConfigError("MOLTHOUSE_HOST_HOME must be an absolute path")
    try:
        resolved = host_home.resolve(strict=True)
    except FileNotFoundError as err:
        raise ConfigError(f"MOLTHOUSE_HOST_HOME does not exist: {host_home}") from err
    if not resolved.is_dir():
        raise ConfigError(f"MOLTHOUSE_HOST_HOME must be a directory: {resolved}")
    return resolved


def normalize_share_source(source: str, *, context: str) -> Path:
    host_home = read_host_share_home()
    source = source.strip()
    if source == "":
        raise ConfigError(f"{context} must not be empty")

    source_path = expand_host_share_source(source, host_home)
    if not source_path.is_absolute():
        raise ConfigError(f"{context} must be an absolute path inside {host_home}")

    try:
        resolved = source_path.resolve(strict=True)
    except FileNotFoundError as err:
        raise ConfigError(f"{context} must point to an existing directory") from err

    if not resolved.is_dir():
        raise ConfigError(f"{context} must point to a directory")

    try:
        relative = resolved.relative_to(host_home)
    except ValueError as err:
        raise ConfigError(f"{context} must stay inside {host_home}") from err

    if len(relative.parts) == 0:
        raise ConfigError(f"{context} must not be the host home itself: {host_home}")

    return resolved


def normalize_mount_point(mount_point: str, *, context: str) -> str:
    normalized = posixpath.normpath(mount_point.strip())
    if normalized == ".":
        raise ConfigError(f"{context} must not be empty")
    if not normalized.startswith("/"):
        raise ConfigError(f"{context} must be an absolute path")
    if normalized == "/":
        raise ConfigError(f"{context} must not be '/'")

    path = PurePosixPath(normalized)
    if any(part in ("", ".", "..") for part in path.parts):
        raise ConfigError(f"{context} contains an invalid path segment")

    reserved_roots = {
        "/dev",
        "/proc",
        "/sys",
    }
    for reserved in reserved_roots:
        if normalized == reserved or normalized.startswith(f"{reserved}/"):
            raise ConfigError(f"{context} must not target {reserved}")

    return normalized


def derived_guest_mount_point_for_source(source_path: Path, *, context: str) -> str:
    host_home = read_host_share_home()
    relative = source_path.relative_to(host_home)
    mount_path = PurePosixPath(normalize_mount_point(GUEST_SHARE_HOME, context="MOLTHOUSE_GUEST_HOME"))
    return normalize_mount_point(str(mount_path.joinpath(*relative.parts)), context=context)


def prepare_requested_share(
    source: str,
    mount_point: str,
    read_only: bool,
    *,
    share_id: str,
    context: str,
) -> dict[str, Any]:
    source_path = normalize_share_source(source, context=f"{context}.source")
    derived_mount_point = derived_guest_mount_point_for_source(
        source_path, context=f"{context}.mount_point"
    )

    requested_mount_point = mount_point.strip()
    if requested_mount_point != "":
        normalized_mount_point = normalize_mount_point(
            requested_mount_point, context=f"{context}.mount_point"
        )
        if normalized_mount_point != derived_mount_point:
            raise ConfigError(
                f"{context}.mount_point must match the automatic VM path: {derived_mount_point}"
            )

    return {
        "id": share_id,
        "source": str(source_path),
        "mount_point": derived_mount_point,
        "read_only": read_only,
    }


def normalize_share(index: int, raw_share: Any) -> dict[str, Any]:
    if not isinstance(raw_share, dict):
        raise ConfigError(f"shares[{index}] must be an object")

    share_id = optional_string(raw_share, "id")
    source = require_string(raw_share, "source")
    mount_point = normalize_mount_point(
        require_string(raw_share, "mount_point"),
        context=f"shares[{index}].mount_point",
    )

    if share_id is None:
        share_id = f"share-{index + 1}"

    return {
        "id": share_id,
        "source": source,
        "mount_point": mount_point,
        "read_only": optional_bool(raw_share, "read_only", True),
    }


def normalize_config(raw_config: Any) -> dict[str, Any]:
    if not isinstance(raw_config, dict):
        raise ConfigError("config must be a JSON object")

    version = raw_config.get("version")
    if version != 1:
        raise ConfigError("version must be 1")

    runtime_support = require_dict(raw_config, "runtime_support")
    vm = require_dict(raw_config, "vm")
    guest = require_dict(raw_config, "guest")
    runtime = require_dict(raw_config, "runtime")

    shares = raw_config.get("shares", [])
    if not isinstance(shares, list):
        raise ConfigError("shares must be a list")

    normalized_shares = [
        normalize_share(index, share) for index, share in enumerate(shares)
    ]

    qmp_socket_path = (
        optional_string(runtime, "qmp_socket_path")
        or optional_string(runtime, "api_socket_path")
        or env_or_default("MOLTHOUSE_QMP_SOCKET", str(RUNTIME_DIR / "qmp.sock"))
    )
    guest_mount_port = runtime.get("guest_mount_port", GUEST_MOUNT_PORT)
    guest_vsock_cid = runtime.get("guest_vsock_cid", GUEST_VSOCK_CID)
    if not isinstance(guest_mount_port, int) or guest_mount_port < 1:
        raise ConfigError("runtime.guest_mount_port must be a positive integer")
    if not isinstance(guest_vsock_cid, int) or guest_vsock_cid < 3:
        raise ConfigError("runtime.guest_vsock_cid must be an integer greater than 2")

    share_ids: set[str] = set()
    mount_points: set[str] = set()
    for share in normalized_shares:
        share_id = share["id"]
        mount_point = share["mount_point"]
        source_path = Path(share["source"])

        if share_id in share_ids:
            raise ConfigError(f"duplicate share id: {share_id}")
        share_ids.add(share_id)

        if mount_point in mount_points:
            raise ConfigError(f"duplicate share mount point: {mount_point}")
        mount_points.add(mount_point)

        if not source_path.is_absolute():
            raise ConfigError(f"share source must be an absolute path: {source_path}")
        if not source_path.is_dir():
            raise ConfigError(
                f"share source must be an existing directory: {source_path}"
            )

    return {
        "version": 1,
        "runtime_support": {
            "assets_dir": optional_string(runtime_support, "assets_dir"),
            "qemu_binary": (
                optional_string(runtime_support, "qemu_binary")
                or optional_string(runtime_support, "cloud_hypervisor_binary")
                or env_or_default("MOLTHOUSE_QEMU", "qemu-system-x86_64")
            ),
            "virtiofsd_binary": require_string(runtime_support, "virtiofsd_binary"),
        },
        "vm": {
            "name": require_string(vm, "name"),
            "boot_vcpus": require_positive_int(vm, "boot_vcpus"),
            "max_vcpus": require_positive_int(vm, "max_vcpus"),
            "memory_mib": require_positive_int(vm, "memory_mib"),
        },
        "guest": {
            "kernel": optional_string(guest, "kernel"),
            "initramfs": optional_string(guest, "initramfs"),
            "disk": optional_string(guest, "disk"),
            "cmdline": require_string(guest, "cmdline"),
        },
        "runtime": {
            "qmp_socket_path": qmp_socket_path,
            "console_socket_path": require_string(runtime, "console_socket_path"),
            "guest_mount_port": guest_mount_port,
            "guest_vsock_cid": guest_vsock_cid,
            "writable_disk_path": require_string(runtime, "writable_disk_path"),
            "writable_disk_size_mib": require_positive_int(
                runtime, "writable_disk_size_mib"
            ),
        },
        "shares": normalized_shares,
    }


def save_config(config: dict[str, Any]) -> dict[str, Any]:
    normalized = normalize_config(config)
    write_json(CONFIG_PATH, normalized)
    return normalized


def load_json_file(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def serialize_json(payload: Any) -> str:
    return json.dumps(payload, indent=2, sort_keys=True)


def share_signature(share: dict[str, Any]) -> tuple[str, str, str, bool]:
    return (
        share["id"],
        share["source"],
        share["mount_point"],
        bool(share.get("read_only", True)),
    )


def share_signatures(shares: list[dict[str, Any]]) -> list[tuple[str, str, str, bool]]:
    return [share_signature(share) for share in shares]


def load_boot_shares() -> list[dict[str, Any]]:
    if not BOOT_SHARES_PATH.exists():
        return []

    payload = load_json_file(BOOT_SHARES_PATH)
    if not isinstance(payload, list):
        raise ConfigError("boot shares must be a list")
    return [normalize_share(index, share) for index, share in enumerate(payload)]


def write_boot_shares(shares: list[dict[str, Any]]) -> None:
    write_json(BOOT_SHARES_PATH, shares)


def clear_boot_shares() -> None:
    BOOT_SHARES_PATH.unlink(missing_ok=True)


def build_qemu_share_args(
    config: dict[str, Any],
    shares: list[dict[str, Any]],
    *,
    include_memory_backend: bool = True,
) -> list[str]:
    if shares == []:
        return []

    args: list[str] = []
    if include_memory_backend:
        args.extend(
            [
                "-numa",
                "node,memdev=mem",
                "-object",
                f"memory-backend-memfd,id=mem,size={config['vm']['memory_mib']}M,share=on",
            ]
        )

    for index, share in enumerate(shares):
        share_id = share["id"]
        args.extend(
            [
                "-chardev",
                f"socket,id=fs{index},path={share_socket_path(share_id)}",
                "-device",
                f"vhost-user-fs-device,chardev=fs{index},tag={share_qmp_tag(share_id)}",
            ]
        )

    return args


def render_qemu_share_args(config: dict[str, Any], shares: list[dict[str, Any]]) -> str:
    return shlex.join(build_qemu_share_args(config, shares))


def append_log(level: str, message: str, details: dict[str, Any] | None = None) -> None:
    ensure_runtime_dirs()
    record: dict[str, Any] = {
        "timestamp": now_iso(),
        "level": level,
        "message": message,
    }
    if details:
        record["details"] = details

    with LOG_PATH.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(record, sort_keys=True) + "\n")


def recent_log_lines(lines: int) -> list[str]:
    ensure_runtime_dirs()
    if not LOG_PATH.exists():
        return []

    requested = max(1, lines)
    content = LOG_PATH.read_text(encoding="utf-8").splitlines()
    return content[-requested:]


def service_managed_vm() -> bool:
    return VM_BACKEND == "systemd-service" and SYSTEMD_VM_SERVICE != ""


def run_systemctl(args: list[str], *, check: bool) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        [SYSTEMCTL_BIN, *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if check and result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip()
        if message == "":
            message = f"{SYSTEMCTL_BIN} {' '.join(args)} failed with exit code {result.returncode}"
        raise RuntimeError(message)
    return result


def systemd_vm_state() -> tuple[str, int | None, str | None]:
    if not service_managed_vm():
        return "stopped", None, None

    result = run_systemctl(
        [
            "show",
            SYSTEMD_VM_SERVICE,
            "--property=ActiveState",
            "--property=SubState",
            "--property=MainPID",
            "--property=Result",
            "--property=ExecMainCode",
            "--property=ExecMainStatus",
        ],
        check=True,
    )

    properties: dict[str, str] = {}
    for line in result.stdout.splitlines():
        key, separator, value = line.partition("=")
        if separator == "":
            continue
        properties[key] = value.strip()

    main_pid = None
    raw_pid = properties.get("MainPID", "0")
    if raw_pid.isdigit() and raw_pid != "0":
        main_pid = int(raw_pid)

    active_state = properties.get("ActiveState", "inactive")
    if active_state == "active":
        return "running", main_pid, None
    if active_state in ("activating", "reloading"):
        return "starting", main_pid, None
    if active_state == "deactivating":
        return "stopping", main_pid, None
    if active_state == "failed":
        result_name = properties.get("Result", "failed")
        exec_code = properties.get("ExecMainCode", "")
        exec_status = properties.get("ExecMainStatus", "")
        details = f"result={result_name}"
        if exec_code or exec_status:
            details = f"{details}, exec={exec_code or '?'}:{exec_status or '?'}"
        return "failed", main_pid, f"{SYSTEMD_VM_SERVICE} failed ({details})"
    return "stopped", main_pid, None


def service_managed_runtime_details(config: dict[str, Any]) -> dict[str, Any]:
    vm_name = config["vm"]["name"]
    runtime = config["runtime"]
    runner_link = MICROVM_STATE_DIR / vm_name / "current"
    runner_script = runner_link / "bin" / "microvm-run"

    details: dict[str, Any] = {
        "microvm_state_dir": str(MICROVM_STATE_DIR),
        "current_runner_path": str(runner_link),
        "runner_script": str(runner_script),
        "console_socket_path": runtime["console_socket_path"],
        "qmp_socket_path": runtime["qmp_socket_path"],
        "systemd_service": SYSTEMD_VM_SERVICE,
        "systemctl_binary": SYSTEMCTL_BIN,
    }

    if runner_link.is_symlink():
        try:
            details["current_runner_target"] = os.readlink(runner_link)
        except OSError as err:
            details["current_runner_target_error"] = str(err)

    details["runner_script_exists"] = runner_script.exists()
    return details


def load_state_payload() -> dict[str, Any]:
    if not STATE_PATH.exists():
        ensure_state()
    payload = load_json_file(STATE_PATH)
    try:
        payload["launch_plan"] = load_launch_plan()
    except Exception as err:
        payload["launch_plan_error"] = str(err)
    return payload


def load_launch_plan() -> dict[str, Any]:
    if not LAUNCH_PLAN_PATH.exists():
        ensure_state()
    return load_json_file(LAUNCH_PLAN_PATH)


def console_state(
    config: dict[str, Any], state_payload: dict[str, Any] | None = None
) -> dict[str, Any]:
    runtime = config["runtime"]
    console_path = Path(runtime["console_socket_path"])
    status = "unknown"
    if state_payload is not None:
        status = str(state_payload.get("status", "unknown"))

    if status == "running" and console_path.exists():
        return {
            "available": True,
            "path": runtime["console_socket_path"],
            "transport": "serial-socket",
            "message": "Serial console socket is ready.",
        }

    if status == "running":
        return {
            "available": False,
            "path": runtime["console_socket_path"],
            "transport": "serial-socket",
            "message": "MoltHouse is waiting for the serial console socket to appear.",
        }

    return {
        "available": False,
        "path": runtime["console_socket_path"],
        "transport": "serial-socket",
        "message": "Start the VM to make the serial console available.",
    }


def generate_share_id(existing_shares: list[dict[str, Any]]) -> str:
    existing = {share["id"] for share in existing_shares}
    index = 1
    while True:
        candidate = f"share-{index}"
        if candidate not in existing:
            return candidate
        index += 1


def share_socket_path(share_id: str) -> Path:
    return RUNTIME_DIR / f"virtiofsd-{share_id}.sock"


def remove_path(path: Path) -> None:
    path.unlink(missing_ok=True)


def ensure_console_socket_permissions(path: Path) -> None:
    try:
        os.chmod(path, 0o666)
    except FileNotFoundError:
        return


def terminate_process(process: subprocess.Popen[bytes], name: str) -> None:
    if process.poll() is not None:
        return

    append_log("INFO", f"Stopping {name}", {"pid": process.pid})
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return
        time.sleep(0.1)

    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return


def wait_for_path(path: Path, process: subprocess.Popen[bytes], timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return True
        if process.poll() is not None:
            return False
        time.sleep(0.1)
    return path.exists()


def open_log_handle() -> Any:
    ensure_runtime_dirs()
    return LOG_PATH.open("ab", buffering=0)


class QMPError(RuntimeError):
    pass


class QMPConnection:
    def __init__(self, socket_path: Path, timeout: float = 5) -> None:
        self.socket_path = socket_path
        self.timeout = timeout
        self.sock: socket.socket | None = None
        self.reader: Any = None
        self.writer: Any = None
        self.events: list[dict[str, Any]] = []

    def __enter__(self) -> "QMPConnection":
        self.connect()
        return self

    def __exit__(self, _exc_type: Any, _exc: Any, _tb: Any) -> None:
        self.close()

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(self.socket_path))
        self.reader = self.sock.makefile("r", encoding="utf-8")
        self.writer = self.sock.makefile("w", encoding="utf-8")
        self._read_message()
        self.execute("qmp_capabilities")

    def close(self) -> None:
        if self.reader is not None:
            self.reader.close()
            self.reader = None
        if self.writer is not None:
            self.writer.close()
            self.writer = None
        if self.sock is not None:
            self.sock.close()
            self.sock = None

    def _read_message(self) -> dict[str, Any]:
        if self.reader is None:
            raise QMPError("QMP connection is not open")
        line = self.reader.readline()
        if line == "":
            raise QMPError("QMP connection closed unexpectedly")
        return json.loads(line)

    def _send_message(self, payload: dict[str, Any]) -> None:
        if self.writer is None:
            raise QMPError("QMP connection is not open")
        self.writer.write(json.dumps(payload, sort_keys=True) + "\n")
        self.writer.flush()

    def execute(
        self, command: str, arguments: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        payload: dict[str, Any] = {"execute": command}
        if arguments:
            payload["arguments"] = arguments
        self._send_message(payload)

        while True:
            message = self._read_message()
            if "event" in message:
                self.events.append(message)
                continue
            if "error" in message:
                error = message["error"]
                desc = error.get("desc") if isinstance(error, dict) else str(error)
                raise QMPError(desc or f"QMP command failed: {command}")
            if "return" in message:
                return message["return"]

    def wait_for_event(
        self,
        event_name: str,
        *,
        predicate: Any = None,
        timeout: float = 5,
    ) -> dict[str, Any]:
        deadline = time.monotonic() + timeout

        while True:
            for index, event in enumerate(list(self.events)):
                if event.get("event") != event_name:
                    continue
                if predicate is not None and not predicate(event):
                    continue
                self.events.pop(index)
                return event

            if time.monotonic() >= deadline:
                raise QMPError(f"timed out waiting for QMP event: {event_name}")
            message = self._read_message()
            if "event" in message:
                self.events.append(message)


def share_qmp_chardev_id(share_id: str) -> str:
    return f"{QMP_CHARDEV_PREFIX}{share_id}"


def share_qmp_device_id(share_id: str) -> str:
    return f"{QMP_DEVICE_PREFIX}{share_id}"


def share_qmp_tag(share_id: str) -> str:
    return share_id


def read_qmp_socket_path(config: dict[str, Any]) -> Path:
    return Path(config["runtime"]["qmp_socket_path"])


def guest_mount_request(config: dict[str, Any], payload: dict[str, Any]) -> dict[str, Any]:
    runtime = config["runtime"]
    cid = int(runtime["guest_vsock_cid"])
    port = int(runtime["guest_mount_port"])

    if not hasattr(socket, "AF_VSOCK"):
        raise RuntimeError("AF_VSOCK is not available on this host kernel")

    sock = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    sock.settimeout(10)
    try:
        sock.connect((cid, port))
        writer = sock.makefile("w", encoding="utf-8")
        reader = sock.makefile("r", encoding="utf-8")
        writer.write(json.dumps(payload, sort_keys=True) + "\n")
        writer.flush()
        line = reader.readline()
        if line == "":
            raise RuntimeError("guest mount helper closed the connection")
        response = json.loads(line)
        if not response.get("ok", False):
            raise RuntimeError(str(response.get("message", "guest mount helper failed")))
        return response
    finally:
        sock.close()


def wait_for_unix_socket(path: Path, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return True
        time.sleep(0.1)
    return path.exists()


def build_qemu_command(config: dict[str, Any]) -> list[str]:
    runtime_support = config["runtime_support"]
    runtime = config["runtime"]
    vm = config["vm"]
    guest = config["guest"]
    shares = load_boot_shares() or config["shares"]

    command = [
        runtime_support["qemu_binary"],
        "-name",
        vm["name"],
        "-M",
        "microvm,accel=kvm:tcg,acpi=on,pcie=on,pit=off,pic=off,rtc=on,mem-merge=on",
        "-m",
        f"{vm['memory_mib']}M",
        "-smp",
        str(vm["boot_vcpus"]),
        "-nodefaults",
        "-no-user-config",
        "-no-reboot",
        "-enable-kvm",
        "-cpu",
        "host,+x2apic,-sgx",
        "-object",
        f"memory-backend-memfd,id=mem,size={vm['memory_mib']}M,share=on",
        "-numa",
        "node,memdev=mem",
        "-qmp",
        f"unix:{runtime['qmp_socket_path']},server=on,wait=off",
        "-chardev",
        f"socket,id=console0,path={runtime['console_socket_path']},server=on,wait=off",
        "-serial",
        "chardev:console0",
        "-device",
        "virtio-rng-pci",
        "-display",
        "none",
        "-nographic",
    ]

    if guest["kernel"] is not None:
        command.extend(["-kernel", guest["kernel"], "-append", guest["cmdline"]])
        if guest["initramfs"] is not None:
            command.extend(["-initrd", guest["initramfs"]])

    if guest["disk"] is not None:
        command.extend(
            [
                "-drive",
                f"id=osdisk,format=raw,file={guest['disk']},if=none,aio=io_uring,discard=unmap,read-only=off",
                "-device",
                "virtio-blk-pci,drive=osdisk,serial=osdisk",
            ]
        )

    command.extend(
        [
            "-drive",
            f"id=runtime,format=raw,file={runtime['writable_disk_path']},if=none,aio=io_uring,discard=unmap,read-only=off",
            "-device",
            "virtio-blk-pci,drive=runtime,serial=runtime",
        ]
    )

    command.extend(build_qemu_share_args(config, shares, include_memory_backend=False))

    return command


def load_config() -> dict[str, Any]:
    if not CONFIG_PATH.exists():
        config = default_config()
        write_json(CONFIG_PATH, config)
        return config

    raw_config = json.loads(CONFIG_PATH.read_text(encoding="utf-8"))
    return normalize_config(raw_config)


def ensure_sparse_file(path: Path, size_mib: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    size_bytes = size_mib * 1024 * 1024
    with path.open("ab"):
        pass
    current_size = path.stat().st_size
    if current_size >= size_bytes:
        return
    with path.open("r+b") as handle:
        handle.truncate(size_bytes)


def runtime_paths(config: dict[str, Any]) -> dict[str, str]:
    runtime = config["runtime"]
    runtime_support = config["runtime_support"]
    paths = {
        "config": str(CONFIG_PATH),
        "log": str(LOG_PATH),
        "boot_shares": str(BOOT_SHARES_PATH),
        "state": str(STATE_PATH),
        "launch_plan": str(LAUNCH_PLAN_PATH),
        "vm_runtime": str(VM_RUNTIME_PATH),
        "qmp_socket": runtime["qmp_socket_path"],
        "console_socket": runtime["console_socket_path"],
        "writable_disk": runtime["writable_disk_path"],
        "qemu_binary": runtime_support["qemu_binary"],
        "virtiofsd_binary": runtime_support["virtiofsd_binary"],
    }

    if service_managed_vm():
        paths["vm_service"] = SYSTEMD_VM_SERVICE
        paths["systemctl_binary"] = SYSTEMCTL_BIN

    return paths


def guest_blockers(config: dict[str, Any]) -> list[str]:
    if service_managed_vm():
        if SYSTEMD_VM_SERVICE == "":
            return ["Set MOLTHOUSE_VM_SYSTEMD_SERVICE before starting the VM."]
        return []

    blockers: list[str] = []
    guest = config["guest"]

    kernel = guest["kernel"]
    initramfs = guest["initramfs"]
    disk = guest["disk"]

    if kernel is None and disk is None:
        blockers.append(
            f"Set guest.kernel or guest.disk in {CONFIG_PATH} before starting the VM."
        )

    for label, value in (
        ("guest.kernel", kernel),
        ("guest.initramfs", initramfs),
        ("guest.disk", disk),
    ):
        if value is None:
            continue
        if not Path(value).exists():
            blockers.append(f"{label} does not exist: {value}")

    return blockers


def build_vm_runtime_description(config: dict[str, Any]) -> dict[str, Any]:
    vm = config["vm"]
    guest = config["guest"]
    runtime = config["runtime"]

    return {
        "backend": "qemu",
        "qmp_socket_path": runtime["qmp_socket_path"],
        "console_socket_path": runtime["console_socket_path"],
        "cpus": {
            "boot_vcpus": vm["boot_vcpus"],
            "max_vcpus": vm["max_vcpus"],
        },
        "memory_mib": vm["memory_mib"],
        "guest": {
            "kernel": guest["kernel"],
            "initramfs": guest["initramfs"],
            "disk": guest["disk"],
            "cmdline": guest["cmdline"],
        },
    }


def build_launch_plan(config: dict[str, Any], blockers: list[str]) -> dict[str, Any]:
    runtime = config["runtime"]
    runtime_support = config["runtime_support"]
    if service_managed_vm():
        return {
            "version": 1,
            "generated_at": now_iso(),
            "backend": "systemd-service",
            "boot_ready": len(blockers) == 0,
            "boot_blockers": blockers,
            "systemd_service": SYSTEMD_VM_SERVICE,
            "systemctl_binary": SYSTEMCTL_BIN,
            "runtime_diagnostics": service_managed_runtime_details(config),
        }

    return {
        "version": 1,
        "generated_at": now_iso(),
        "boot_ready": len(blockers) == 0,
        "boot_blockers": blockers,
        "qmp_socket_path": runtime["qmp_socket_path"],
        "console_socket_path": runtime["console_socket_path"],
        "qemu_binary": runtime_support["qemu_binary"],
        "virtiofsd_binary": runtime_support["virtiofsd_binary"],
        "vm_runtime_path": str(VM_RUNTIME_PATH),
        "qemu_command": build_qemu_command(config),
    }


def render_runtime_artifacts(
    config: dict[str, Any],
) -> tuple[dict[str, Any], dict[str, Any], list[str]]:
    if service_managed_vm():
        blockers = guest_blockers(config)
        vm_config = {
            "backend": "systemd-service",
            "service": SYSTEMD_VM_SERVICE,
        }
        launch_plan = build_launch_plan(config, blockers)

        write_json(VM_RUNTIME_PATH, vm_config)
        write_json(LAUNCH_PLAN_PATH, launch_plan)
        return vm_config, launch_plan, blockers

    runtime = config["runtime"]
    ensure_sparse_file(
        Path(runtime["writable_disk_path"]), runtime["writable_disk_size_mib"]
    )

    blockers = guest_blockers(config)
    vm_config = build_vm_runtime_description(config)
    launch_plan = build_launch_plan(config, blockers)

    write_json(VM_RUNTIME_PATH, vm_config)
    write_json(LAUNCH_PLAN_PATH, launch_plan)

    return vm_config, launch_plan, blockers


def write_state(
    status: str,
    config: dict[str, Any] | None,
    blockers: list[str],
    last_error: str | None,
    *,
    write_pid: bool = True,
    vm_pid: int | None = None,
    console_available: bool = False,
    applied_shares_count: int = 0,
) -> None:
    payload = {
        "version": 1,
        "status": status,
        "updated_at": now_iso(),
        "last_error": last_error,
        "service": {
            "pid": os.getpid(),
        },
    }

    if config is not None:
        vm = config["vm"]
        boot_signatures = share_signatures(load_boot_shares())
        configured_signatures = share_signatures(config["shares"])
        payload["paths"] = runtime_paths(config)
        payload["vm"] = {
            "name": vm["name"],
            "applied_shares_count": applied_shares_count,
            "boot_ready": len(blockers) == 0,
            "boot_blockers": blockers,
            "console_available": console_available,
            "pid": vm_pid,
            "restart_required": status == "running"
            and configured_signatures != boot_signatures,
            "share_hotplug_enabled": False,
            "shares_count": len(config["shares"]),
        }

    write_json(STATE_PATH, payload)
    if write_pid:
        PID_PATH.write_text(f"{os.getpid()}\n", encoding="utf-8")


def ensure_state() -> tuple[dict[str, Any], list[str]]:
    ensure_runtime_dirs()
    config = load_config()
    _, _, blockers = render_runtime_artifacts(config)
    last_error = blockers[0] if blockers else None
    write_state("stopped", config, blockers, last_error)
    return config, blockers


class MolthouseDBusService:
    def __init__(self) -> None:
        import gi

        gi.require_version("Gio", "2.0")
        from gi.repository import Gio, GLib

        self.Gio = Gio
        self.GLib = GLib
        self.loop = GLib.MainLoop()
        self.node_info = Gio.DBusNodeInfo.new_for_xml(INTROSPECTION_XML)
        self.connection: Any = None
        self.registration_id = 0
        self.owner_id = 0
        self.last_config_mtime: float | None = None
        self.last_state_json = ""
        self.last_shares_json = ""
        self.last_failure_json = ""
        self.stopping = False
        self.exit_code = 0
        self.vm_process: subprocess.Popen[bytes] | None = None
        self.virtiofsd_processes: dict[str, subprocess.Popen[bytes]] = {}
        self.active_shares: dict[str, dict[str, Any]] = {}
        self.applied_share_signatures: list[tuple[str, str, str, bool]] = []

    def _variant(self, payload: str) -> Any:
        return self.GLib.Variant("(s)", (payload,))

    def _emit_signal(self, signal_name: str, payload: str) -> None:
        if self.connection is None:
            return

        self.connection.emit_signal(
            None,
            OBJECT_PATH,
            INTERFACE_NAME,
            signal_name,
            self._variant(payload),
        )

    def _load_config_mtime(self) -> float | None:
        if not CONFIG_PATH.exists():
            return None
        return CONFIG_PATH.stat().st_mtime

    def _share_payload(self, share: dict[str, Any]) -> dict[str, Any]:
        payload = dict(share)
        runtime_details = self.active_shares.get(share["id"])
        if runtime_details is None:
            payload["status"] = "configured" if not self._vm_running() else "pending"
            return payload

        active_share = runtime_details.get("share")
        if not isinstance(active_share, dict) or share_signature(active_share) != share_signature(
            share
        ):
            payload["status"] = "pending"
            return payload

        payload["status"] = runtime_details.get("status", "configured")
        last_error = runtime_details.get("last_error")
        if isinstance(last_error, str) and last_error != "":
            payload["last_error"] = last_error
        return payload

    def _shares_payload(self) -> dict[str, Any]:
        config = load_config()
        return {"shares": [self._share_payload(share) for share in config["shares"]]}

    def _emit_runtime_signals(self) -> None:
        state_payload = load_state_payload()
        state_json = serialize_json(state_payload)
        if state_json != self.last_state_json:
            self.last_state_json = state_json
            self._emit_signal("StateChanged", state_json)

        try:
            shares_payload = self._shares_payload()
        except Exception as err:
            shares_payload = {
                "shares": [],
                "error": str(err),
            }
        shares_json = serialize_json(shares_payload)
        if shares_json != self.last_shares_json:
            self.last_shares_json = shares_json
            self._emit_signal("SharesChanged", shares_json)

        failure_json = ""
        if state_payload.get("status") == "failed" or state_payload.get("last_error"):
            failure_json = serialize_json(
                {
                    "status": state_payload.get("status"),
                    "last_error": state_payload.get("last_error"),
                    "updated_at": state_payload.get("updated_at"),
                }
            )
        if failure_json and failure_json != self.last_failure_json:
            self.last_failure_json = failure_json
            self._emit_signal("VmFailed", failure_json)

    def _write_runtime_state(
        self,
        status: str,
        *,
        last_error: str | None = None,
        write_pid: bool = True,
    ) -> tuple[dict[str, Any], list[str]]:
        config = load_config()
        _, _, blockers = render_runtime_artifacts(config)
        derived_error = last_error
        if derived_error is None and status != "running":
            derived_error = blockers[0] if blockers else None

        write_state(
            status,
            config,
            blockers,
            derived_error,
            applied_shares_count=len(self.applied_share_signatures),
            write_pid=write_pid,
            vm_pid=self._vm_pid(),
            console_available=self._console_socket_available(),
        )
        return config, blockers

    def _vm_pid(self) -> int | None:
        if service_managed_vm():
            _status, vm_pid, _last_error = systemd_vm_state()
            return vm_pid
        if self.vm_process is None:
            return None
        if self.vm_process.poll() is not None:
            return None
        return self.vm_process.pid

    def _vm_running(self) -> bool:
        if service_managed_vm():
            status, _vm_pid, _last_error = systemd_vm_state()
            return status == "running"
        return self._vm_pid() is not None

    def _console_socket_available(self) -> bool:
        if not self._vm_running():
            return False
        config = load_config()
        console_path = Path(config["runtime"]["console_socket_path"])
        ensure_console_socket_permissions(console_path)
        return console_path.exists()

    def _cleanup_runtime_paths(self) -> None:
        qmp_socket: Path | None = None
        console_socket: Path | None = None

        try:
            config = load_config()
            qmp_socket = Path(config["runtime"]["qmp_socket_path"])
            console_socket = Path(config["runtime"]["console_socket_path"])
        except Exception:
            if STATE_PATH.exists():
                state_payload = load_state_payload()
                paths = state_payload.get("paths", {})
                if isinstance(paths, dict):
                    qmp_socket_value = paths.get("qmp_socket")
                    console_socket_value = paths.get("console_socket")
                    if isinstance(qmp_socket_value, str):
                        qmp_socket = Path(qmp_socket_value)
                    if isinstance(console_socket_value, str):
                        console_socket = Path(console_socket_value)

        if qmp_socket is not None:
            remove_path(qmp_socket)
        if console_socket is not None:
            remove_path(console_socket)

        for socket_path in RUNTIME_DIR.glob("virtiofsd-*.sock"):
            remove_path(socket_path)

    def _wait_for_qmp_socket(self, config: dict[str, Any], timeout: float = 10) -> None:
        qmp_socket = read_qmp_socket_path(config)
        if wait_for_unix_socket(qmp_socket, timeout):
            return
        raise RuntimeError(f"QEMU did not create its QMP socket: {qmp_socket}")

    def _start_virtiofsd_process(self, config: dict[str, Any], share: dict[str, Any]) -> None:
        share_id = share["id"]
        existing = self.virtiofsd_processes.get(share_id)
        if existing is not None and existing.poll() is None:
            return

        socket_path = share_socket_path(share_id)
        remove_path(socket_path)
        command = [
            config["runtime_support"]["virtiofsd_binary"],
            "--shared-dir",
            share["source"],
            "--socket-path",
            str(socket_path),
            "--socket-group",
            VIRTIOFSD_SOCKET_GROUP,
            "--cache",
            "auto",
            "--log-level",
            "info",
        ]
        if share.get("read_only", True):
            command.append("--readonly")

        log_handle = open_log_handle()
        process = subprocess.Popen(
            command,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        log_handle.close()
        self.virtiofsd_processes[share_id] = process
        if not wait_for_path(socket_path, process, 5):
            raise RuntimeError(f"virtiofsd failed to start for share {share_id}")

    def _stop_virtiofsd_process(self, share_id: str) -> None:
        process = self.virtiofsd_processes.pop(share_id, None)
        if process is not None:
            terminate_process(process, f"virtiofsd:{share_id}")
        remove_path(share_socket_path(share_id))

    def _managed_qmp_state(self, qmp: QMPConnection) -> tuple[set[str], set[str]]:
        device_ids: set[str] = set()
        pci_buses = qmp.execute("query-pci")
        if isinstance(pci_buses, list):
            for bus in pci_buses:
                if not isinstance(bus, dict):
                    continue
                for device in bus.get("devices", []):
                    if not isinstance(device, dict):
                        continue
                    qdev_id = str(device.get("qdev_id", "")).strip()
                    if qdev_id.startswith(QMP_DEVICE_PREFIX):
                        device_ids.add(qdev_id)

        chardev_ids: set[str] = set()
        chardevs = qmp.execute("query-chardev")
        if isinstance(chardevs, list):
            for chardev in chardevs:
                if not isinstance(chardev, dict):
                    continue
                label = str(chardev.get("label", "")).strip()
                if label.startswith(QMP_CHARDEV_PREFIX):
                    chardev_ids.add(label)

        return device_ids, chardev_ids

    def _guest_mount_share(self, config: dict[str, Any], share: dict[str, Any]) -> None:
        payload = {
            "action": "mount",
            "mount_point": share["mount_point"],
            "read_only": share.get("read_only", True),
            "tag": share_qmp_tag(share["id"]),
        }
        last_error: Exception | None = None
        for _attempt in range(20):
            try:
                guest_mount_request(config, payload)
                return
            except Exception as err:
                last_error = err
                time.sleep(0.25)

        if last_error is not None:
            raise last_error
        raise RuntimeError("guest mount helper did not mount the share")

    def _guest_unmount_share(self, config: dict[str, Any], share: dict[str, Any]) -> None:
        guest_mount_request(
            config,
            {
                "action": "unmount",
                "mount_point": share["mount_point"],
                "tag": share_qmp_tag(share["id"]),
            },
        )

    def _boot_shares(self) -> list[dict[str, Any]]:
        return load_boot_shares()

    def _clear_share_runtime(self) -> None:
        self._stop_virtiofsd_processes()
        self.active_shares.clear()
        self.applied_share_signatures = []
        clear_boot_shares()

    def _prepare_boot_shares(self, config: dict[str, Any]) -> list[dict[str, Any]]:
        shares = [dict(share) for share in config["shares"]]
        write_boot_shares(shares)
        self._stop_virtiofsd_processes()
        for share in shares:
            self._start_virtiofsd_process(config, share)
        return shares

    def _start_boot_share_processes(self, config: dict[str, Any]) -> None:
        for share in self._boot_shares():
            self._start_virtiofsd_process(config, share)

    def _reconcile_boot_shares(self, config: dict[str, Any]) -> None:
        if not self._vm_running():
            self.active_shares.clear()
            self.applied_share_signatures = []
            return

        boot_shares = self._boot_shares()
        self.active_shares.clear()
        applied_signatures: list[tuple[str, str, str, bool]] = []
        failures: list[str] = []

        for share in boot_shares:
            status = "active"
            last_error: str | None = None
            try:
                self._guest_mount_share(config, share)
                applied_signatures.append(share_signature(share))
            except Exception as err:
                status = "failed"
                last_error = str(err)
                failures.append(f"{share['id']}: {err}")

            self.active_shares[share["id"]] = {
                "share": dict(share),
                "status": status,
                "last_error": last_error,
            }

        self.applied_share_signatures = applied_signatures

        if failures:
            raise RuntimeError("Failed to mount boot shares: " + "; ".join(failures))

    def _stop_virtiofsd_processes(self) -> None:
        for share_id in list(self.virtiofsd_processes.keys()):
            self._stop_virtiofsd_process(share_id)

    def _start_vm_process(self, config: dict[str, Any]) -> None:
        command = build_qemu_command(config)
        log_handle = open_log_handle()
        self.vm_process = subprocess.Popen(
            command,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        log_handle.close()

        runtime = config["runtime"]
        qmp_socket = Path(runtime["qmp_socket_path"])
        console_socket = Path(runtime["console_socket_path"])
        if not wait_for_path(qmp_socket, self.vm_process, 5):
            raise RuntimeError("QEMU did not create its QMP socket")
        wait_for_path(console_socket, self.vm_process, 5)

    def _stop_vm_process(self) -> None:
        if self.vm_process is not None:
            terminate_process(self.vm_process, "qemu")
        self.vm_process = None
        self.active_shares.clear()
        self.applied_share_signatures = []

    def _start_vm(self) -> dict[str, Any]:
        if service_managed_vm():
            status, _vm_pid, _last_error = systemd_vm_state()
            if status == "running":
                return {
                    "ok": True,
                    "message": "VM is already running.",
                    "state": load_state_payload(),
                }

            self._write_runtime_state("starting", last_error=None)
            append_log(
                "INFO",
                "Starting MoltHouse VM",
                {"service": SYSTEMD_VM_SERVICE, "backend": "systemd-service"},
            )

            try:
                config = load_config()
                self._prepare_boot_shares(config)
                run_systemctl(["start", SYSTEMD_VM_SERVICE], check=True)
                self._wait_for_qmp_socket(config)
                self._reconcile_boot_shares(config)
                refresh = self.refresh_runtime("systemctl-start", emit_signals=False)
                state_payload = refresh.get("state", load_state_payload())
                status = state_payload.get("status")
                if status != "running":
                    message = state_payload.get(
                        "last_error",
                        f"{SYSTEMD_VM_SERVICE} did not report a running state after start.",
                    )
                    self._emit_runtime_signals()
                    return {
                        "ok": False,
                        "code": "start_failed",
                        "message": message,
                        "launch_plan": load_launch_plan(),
                        "state": state_payload,
                    }

                self._emit_runtime_signals()
                return {
                    "ok": True,
                    "message": "VM started.",
                    "launch_plan": load_launch_plan(),
                    "state": state_payload,
                }
            except Exception as err:
                try:
                    run_systemctl(["stop", SYSTEMD_VM_SERVICE], check=False)
                except Exception:
                    pass
                self._clear_share_runtime()
                append_log("ERROR", "Failed to start MoltHouse VM", {"error": str(err)})
                self._write_runtime_state("failed", last_error=str(err))
                self._emit_runtime_signals()
                return {
                    "ok": False,
                    "code": "start_failed",
                    "message": str(err),
                    "launch_plan": load_launch_plan(),
                    "state": load_state_payload(),
                }

        if self._vm_running():
            return {
                "ok": True,
                "message": "VM is already running.",
                "state": load_state_payload(),
            }

        config = load_config()
        _, _, blockers = render_runtime_artifacts(config)
        if blockers:
            self._write_runtime_state("stopped")
            return {
                "ok": False,
                "code": "boot_blocked",
                "message": blockers[0],
                "boot_blockers": blockers,
                "launch_plan": load_launch_plan(),
                "state": load_state_payload(),
            }

        self._cleanup_runtime_paths()
        self._write_runtime_state("starting", last_error=None)
        append_log("INFO", "Starting MoltHouse VM")

        try:
            self._prepare_boot_shares(config)
            self._start_vm_process(config)
            self._reconcile_boot_shares(config)
            self._write_runtime_state("running", last_error=None)
            self._emit_runtime_signals()
            return {
                "ok": True,
                "message": "VM started.",
                "launch_plan": load_launch_plan(),
                "state": load_state_payload(),
            }
        except Exception as err:
            append_log("ERROR", "Failed to start MoltHouse VM", {"error": str(err)})
            self._stop_vm_process()
            self._clear_share_runtime()
            self._cleanup_runtime_paths()
            self._write_runtime_state("failed", last_error=str(err))
            self._emit_runtime_signals()
            return {
                "ok": False,
                "code": "start_failed",
                "message": str(err),
                "launch_plan": load_launch_plan(),
                "state": load_state_payload(),
            }

    def _stop_vm(self) -> dict[str, Any]:
        if service_managed_vm():
            status, _vm_pid, _last_error = systemd_vm_state()
            if status == "stopped":
                self._clear_share_runtime()
                self._write_runtime_state("stopped")
                return {
                    "ok": True,
                    "message": "VM is already stopped.",
                    "state": load_state_payload(),
                }

            self._write_runtime_state("stopping", last_error=None)
            append_log(
                "INFO",
                "Stopping MoltHouse VM",
                {"service": SYSTEMD_VM_SERVICE, "backend": "systemd-service"},
            )

            try:
                run_systemctl(["stop", SYSTEMD_VM_SERVICE], check=True)
                self._clear_share_runtime()
                self._cleanup_runtime_paths()
                refresh = self.refresh_runtime("systemctl-stop", emit_signals=False)
                self._emit_runtime_signals()
                return {
                    "ok": True,
                    "message": "VM stopped.",
                    "state": refresh.get("state", load_state_payload()),
                }
            except Exception as err:
                append_log("ERROR", "Failed to stop MoltHouse VM", {"error": str(err)})
                self._write_runtime_state("failed", last_error=str(err))
                self._emit_runtime_signals()
                return {
                    "ok": False,
                    "code": "stop_failed",
                    "message": str(err),
                    "state": load_state_payload(),
                }

        if not self._vm_running():
            self._clear_share_runtime()
            self._write_runtime_state("stopped")
            return {
                "ok": True,
                "message": "VM is already stopped.",
                "state": load_state_payload(),
            }

        self._write_runtime_state("stopping", last_error=None)
        append_log("INFO", "Stopping MoltHouse VM")
        self._stop_vm_process()
        self._clear_share_runtime()
        self._cleanup_runtime_paths()
        self._write_runtime_state("stopped", last_error=None)
        self._emit_runtime_signals()
        return {
            "ok": True,
            "message": "VM stopped.",
            "state": load_state_payload(),
        }

    def _restart_vm(self) -> dict[str, Any]:
        if service_managed_vm():
            append_log(
                "INFO",
                "Restarting MoltHouse VM",
                {"service": SYSTEMD_VM_SERVICE, "backend": "systemd-service"},
            )

        stop_payload = self._stop_vm()
        if not stop_payload.get("ok", False):
            if service_managed_vm():
                stop_payload["code"] = "restart_failed"
            return stop_payload
        start_payload = self._start_vm()
        if not start_payload.get("ok", False):
            start_payload["code"] = "restart_failed"
            return start_payload

        start_payload["message"] = "VM restarted."
        return start_payload

    def _poll_vm(self) -> bool:
        if service_managed_vm():
            try:
                status, _vm_pid, last_error = systemd_vm_state()
                if status != "running" and (
                    self.virtiofsd_processes
                    or self.active_shares
                    or BOOT_SHARES_PATH.exists()
                ):
                    self._clear_share_runtime()
                current_state = load_state_payload()
                if (
                    current_state.get("status") != status
                    or current_state.get("last_error") != last_error
                ):
                    self._write_runtime_state(status, last_error=last_error)
                    self._emit_runtime_signals()
            except Exception as err:
                current_state = load_state_payload()
                if current_state.get("status") != "failed" or current_state.get(
                    "last_error"
                ) != str(err):
                    write_state("failed", None, [], str(err))
                    append_log(
                        "ERROR",
                        "MoltHouse failed to poll the declarative OpenClaw microVM",
                        {"error": str(err)},
                    )
                    self._emit_runtime_signals()
            return True

        if self.vm_process is not None and self.vm_process.poll() is not None:
            returncode = self.vm_process.returncode
            append_log(
                "ERROR",
                "qemu exited unexpectedly",
                {"returncode": returncode},
            )
            self._clear_share_runtime()
            self.vm_process = None
            self._cleanup_runtime_paths()
            self._write_runtime_state(
                "failed",
                last_error=f"qemu exited unexpectedly with code {returncode}",
            )
            self._emit_runtime_signals()

        for share_id, process in list(self.virtiofsd_processes.items()):
            if process.poll() is None:
                continue
            returncode = process.returncode
            append_log(
                "ERROR",
                "virtiofsd exited unexpectedly",
                {"returncode": returncode, "share_id": share_id},
            )
            self.virtiofsd_processes.pop(share_id, None)
            remove_path(share_socket_path(share_id))
            share_details = self.active_shares.get(share_id)
            if share_details is not None:
                share_details["status"] = "failed"
                share_details["last_error"] = (
                    f"virtiofsd exited unexpectedly for share {share_id}"
                )
            self.applied_share_signatures = [
                signature
                for signature in self.applied_share_signatures
                if signature[0] != share_id
            ]
            if self._vm_running():
                self._write_runtime_state(
                    "failed",
                    last_error=f"virtiofsd exited unexpectedly for share {share_id}",
                )
                self._emit_runtime_signals()

        return True

    def refresh_runtime(
        self, reason: str, *, emit_signals: bool = True
    ) -> dict[str, Any]:
        try:
            last_error = None
            if service_managed_vm():
                status, _vm_pid, last_error = systemd_vm_state()
            else:
                status = "running" if self._vm_running() else "stopped"

            if status == "running" and reason == "service-start":
                self._start_boot_share_processes(load_config())
                self._reconcile_boot_shares(load_config())

            config, blockers = self._write_runtime_state(status, last_error=last_error)
            if blockers:
                append_log(
                    "WARNING",
                    f"MoltHouse runtime refreshed with blockers ({reason})",
                    {"blockers": blockers},
                )
            else:
                append_log("INFO", f"MoltHouse runtime refreshed ({reason})")

            state_payload = load_state_payload()
            if emit_signals:
                self._emit_runtime_signals()
            return {
                "ok": True,
                "state": state_payload,
                "shares": self._shares_payload()["shares"],
                "launch_plan": load_launch_plan(),
            }
        except Exception as err:
            append_log(
                "ERROR",
                f"MoltHouse runtime refresh failed ({reason})",
                {"error": str(err)},
            )
            write_state("failed", None, [], str(err))
            if emit_signals:
                self._emit_runtime_signals()
            return {
                "ok": False,
                "code": "refresh_failed",
                "message": str(err),
                "state": load_state_payload(),
            }

    def _config_changed(self) -> bool:
        current_mtime = self._load_config_mtime()
        if current_mtime is None:
            return False
        if self.last_config_mtime is None or current_mtime != self.last_config_mtime:
            self.last_config_mtime = current_mtime
            return True
        return False

    def _poll_config(self) -> bool:
        if self._config_changed():
            self.refresh_runtime("config-changed")
        return True

    def _handle_stop(self) -> bool:
        self.stop()
        return self.GLib.SOURCE_REMOVE

    def _handle_reload(self) -> bool:
        self.refresh_runtime("signal-hup")
        return self.GLib.SOURCE_CONTINUE

    def stop(self, exit_code: int = 0) -> None:
        if self.stopping:
            if exit_code != 0:
                self.exit_code = exit_code
            return

        self.stopping = True
        self.exit_code = exit_code
        try:
            self._stop_vm_process()
            self._clear_share_runtime()
            self._cleanup_runtime_paths()
            config, blockers = self._write_runtime_state("stopping", last_error=None)
            self._emit_runtime_signals()
            write_state(
                "stopped",
                config,
                blockers,
                None,
                write_pid=False,
                vm_pid=None,
                console_available=False,
            )
            append_log("INFO", "MoltHouse helper stopping")
            self._emit_runtime_signals()
        except Exception as err:
            write_state("failed", None, [], str(err), write_pid=False)
            append_log(
                "ERROR", "MoltHouse helper failed during shutdown", {"error": str(err)}
            )
            self._emit_runtime_signals()
        finally:
            PID_PATH.unlink(missing_ok=True)
            if self.registration_id and self.connection is not None:
                try:
                    self.connection.unregister_object(self.registration_id)
                except Exception:
                    pass
                self.registration_id = 0
            self.owner_id = 0
            self.connection = None
            self.loop.quit()

    def _resolve_bus_type(self) -> Any:
        if BUS_KIND == "session":
            return self.Gio.BusType.SESSION
        return self.Gio.BusType.SYSTEM

    def on_bus_acquired(self, connection: Any, _name: str) -> None:
        self.connection = connection
        interface_info = self.node_info.interfaces[0]
        register_object = getattr(
            connection,
            "register_object_with_closures2",
            connection.register_object,
        )
        self.registration_id = register_object(
            OBJECT_PATH,
            interface_info,
            self.on_method_call,
            None,
            None,
        )

    def on_name_acquired(self, _connection: Any, _name: str) -> None:
        append_log("INFO", "MoltHouse D-Bus name acquired", {"bus_name": BUS_NAME})

    def on_name_lost(self, _connection: Any, _name: str) -> None:
        if self.stopping:
            return
        append_log("ERROR", "MoltHouse D-Bus name lost", {"bus_name": BUS_NAME})
        self.stop(exit_code=1)

    def _response(self, invocation: Any, payload: dict[str, Any]) -> None:
        invocation.return_value(self._variant(serialize_json(payload)))

    def _list_shares_payload(self) -> dict[str, Any]:
        return self._shares_payload()

    def _get_recent_logs_payload(self, lines: int) -> dict[str, Any]:
        safe_lines = max(1, lines)
        return {
            "path": str(LOG_PATH),
            "lines": recent_log_lines(safe_lines),
        }

    def _console_state_payload(self) -> dict[str, Any]:
        config = load_config()
        return console_state(config, load_state_payload())

    def _apply_share_config_change(
        self,
        previous_config: dict[str, Any],
        next_config: dict[str, Any],
        reason: str,
    ) -> dict[str, Any]:
        save_config(next_config)
        refresh = self.refresh_runtime(reason)
        if not refresh.get("ok", False):
            save_config(previous_config)
            raise RuntimeError(str(refresh.get("message", "failed to refresh runtime")))
        return refresh

    def _add_share(
        self, source: str, mount_point: str, read_only: bool
    ) -> dict[str, Any]:
        previous_config = load_config()
        config = dict(previous_config)
        shares = list(previous_config["shares"])
        share = prepare_requested_share(
            source,
            mount_point,
            read_only,
            share_id=generate_share_id(shares),
            context="share",
        )
        shares.append(share)
        config["shares"] = shares
        append_log("INFO", "MoltHouse share added", {"share": share})
        try:
            refresh = self._apply_share_config_change(
                previous_config, config, "share-added"
            )
        except Exception as err:
            append_log("ERROR", "Failed to add MoltHouse share", {"error": str(err)})
            return {
                "ok": False,
                "code": "share_apply_failed",
                "message": str(err),
                "state": load_state_payload(),
            }
        return {
            "ok": True,
            "share": share,
            "state": refresh.get("state"),
        }

    def _update_share(
        self, share_id: str, source: str, mount_point: str, read_only: bool
    ) -> dict[str, Any]:
        previous_config = load_config()
        config = dict(previous_config)
        shares = list(previous_config["shares"])
        updated_share: dict[str, Any] | None = None
        for index, share in enumerate(shares):
            if share["id"] != share_id:
                continue
            updated_share = prepare_requested_share(
                source,
                mount_point,
                read_only,
                share_id=share_id,
                context="share",
            )
            shares[index] = updated_share
            break

        if updated_share is None:
            return {
                "ok": False,
                "code": "share_not_found",
                "message": f"Share not found: {share_id}",
            }

        config["shares"] = shares
        append_log("INFO", "MoltHouse share updated", {"share": updated_share})
        try:
            refresh = self._apply_share_config_change(
                previous_config, config, "share-updated"
            )
        except Exception as err:
            append_log(
                "ERROR", "Failed to update MoltHouse share", {"error": str(err)}
            )
            return {
                "ok": False,
                "code": "share_apply_failed",
                "message": str(err),
                "state": load_state_payload(),
            }
        return {
            "ok": True,
            "share": updated_share,
            "state": refresh.get("state"),
        }

    def _remove_share(self, share_id: str) -> dict[str, Any]:
        previous_config = load_config()
        config = dict(previous_config)
        shares = list(previous_config["shares"])
        remaining = [share for share in shares if share["id"] != share_id]
        if len(remaining) == len(shares):
            return {
                "ok": False,
                "code": "share_not_found",
                "message": f"Share not found: {share_id}",
            }

        config["shares"] = remaining
        append_log("INFO", "MoltHouse share removed", {"share_id": share_id})
        try:
            refresh = self._apply_share_config_change(
                previous_config, config, "share-removed"
            )
        except Exception as err:
            append_log(
                "ERROR", "Failed to remove MoltHouse share", {"error": str(err)}
            )
            return {
                "ok": False,
                "code": "share_apply_failed",
                "message": str(err),
                "state": load_state_payload(),
            }
        return {
            "ok": True,
            "share_id": share_id,
            "state": refresh.get("state"),
        }

    def on_method_call(
        self,
        _connection: Any,
        _sender: str,
        _object_path: str,
        _interface_name: str,
        method_name: str,
        parameters: Any,
        invocation: Any,
    ) -> None:
        try:
            if method_name == "GetState":
                self._response(invocation, load_state_payload())
                return

            if method_name == "StartVm":
                self._response(invocation, self._start_vm())
                return

            if method_name == "StopVm":
                self._response(invocation, self._stop_vm())
                return

            if method_name == "RestartVm":
                self._response(invocation, self._restart_vm())
                return

            if method_name == "ListShares":
                self._response(invocation, self._list_shares_payload())
                return

            if method_name == "AddShare":
                source, mount_point, read_only = parameters.unpack()
                self._response(
                    invocation, self._add_share(source, mount_point, read_only)
                )
                return

            if method_name == "UpdateShare":
                share_id, source, mount_point, read_only = parameters.unpack()
                self._response(
                    invocation,
                    self._update_share(share_id, source, mount_point, read_only),
                )
                return

            if method_name == "RemoveShare":
                (share_id,) = parameters.unpack()
                self._response(invocation, self._remove_share(share_id))
                return

            if method_name == "GetRecentLogs":
                (lines,) = parameters.unpack()
                self._response(invocation, self._get_recent_logs_payload(lines))
                return

            if method_name == "GetConsoleState":
                self._response(invocation, self._console_state_payload())
                return

            invocation.return_dbus_error(
                f"{INTERFACE_NAME}.Error.UnknownMethod",
                f"Unknown method: {method_name}",
            )
        except ConfigError as err:
            append_log(
                "ERROR",
                "MoltHouse D-Bus request failed validation",
                {"error": str(err)},
            )
            self._response(
                invocation,
                {
                    "ok": False,
                    "code": "invalid_config",
                    "message": str(err),
                    "state": load_state_payload(),
                },
            )
        except Exception as err:
            append_log("ERROR", "MoltHouse D-Bus request failed", {"error": str(err)})
            self._response(
                invocation,
                {
                    "ok": False,
                    "code": "internal_error",
                    "message": str(err),
                },
            )

    def serve(self) -> int:
        refresh = self.refresh_runtime("service-start", emit_signals=False)
        if not refresh.get("ok", False):
            append_log("ERROR", "MoltHouse helper failed to initialize")

        self.last_config_mtime = self._load_config_mtime()
        self.GLib.timeout_add_seconds(2, self._poll_config)
        self.GLib.timeout_add_seconds(1, self._poll_vm)
        self.GLib.unix_signal_add(
            self.GLib.PRIORITY_DEFAULT, signal.SIGTERM, self._handle_stop
        )
        self.GLib.unix_signal_add(
            self.GLib.PRIORITY_DEFAULT, signal.SIGINT, self._handle_stop
        )
        self.GLib.unix_signal_add(
            self.GLib.PRIORITY_DEFAULT, signal.SIGHUP, self._handle_reload
        )

        self.owner_id = self.Gio.bus_own_name(
            self._resolve_bus_type(),
            BUS_NAME,
            self.Gio.BusNameOwnerFlags.NONE,
            self.on_bus_acquired,
            self.on_name_acquired,
            self.on_name_lost,
        )
        self.loop.run()
        return self.exit_code


def serve() -> int:
    return MolthouseDBusService().serve()


def print_json(path: Path) -> int:
    ensure_runtime_dirs()
    if not path.exists():
        ensure_state()
    sys.stdout.write(path.read_text(encoding="utf-8"))
    return 0


def read_vm_memory_mib_for_share_args() -> int:
    if not CONFIG_PATH.exists():
        return default_config()["vm"]["memory_mib"]

    payload = load_json_file(CONFIG_PATH)
    if not isinstance(payload, dict):
        raise ConfigError("config must be a JSON object")

    vm = payload.get("vm")
    if not isinstance(vm, dict):
        raise ConfigError("vm must be an object")

    memory_mib = vm.get("memory_mib")
    if not isinstance(memory_mib, int) or memory_mib < 1:
        raise ConfigError("vm.memory_mib must be a positive integer")

    return memory_mib


def print_qemu_share_args() -> int:
    ensure_runtime_dirs()
    shares = load_boot_shares()
    if shares == []:
        sys.stdout.write("\n")
        return 0

    config = {
        "vm": {
            "memory_mib": read_vm_memory_mib_for_share_args(),
        }
    }
    sys.stdout.write(render_qemu_share_args(config, shares) + "\n")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="MoltHouse privileged helper")
    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True

    subparsers.add_parser(
        "ensure-state", help="Ensure MoltHouse config and runtime state exist"
    )
    subparsers.add_parser(
        "render-config", help="Render launch-plan and VM runtime files"
    )
    subparsers.add_parser(
        "print-state", help="Print the current MoltHouse runtime state JSON"
    )
    subparsers.add_parser(
        "print-config", help="Print the current MoltHouse config JSON"
    )
    subparsers.add_parser(
        "print-qemu-share-args",
        help="Print runtime QEMU arguments for boot shares",
    )
    subparsers.add_parser("serve", help="Run the MoltHouse privileged helper loop")

    args = parser.parse_args()

    if args.command == "ensure-state":
        ensure_state()
        return 0

    if args.command == "render-config":
        ensure_state()
        return 0

    if args.command == "print-state":
        return print_json(STATE_PATH)

    if args.command == "print-config":
        ensure_runtime_dirs()
        if not CONFIG_PATH.exists():
            config = default_config()
            write_json(CONFIG_PATH, config)
        return print_json(CONFIG_PATH)

    if args.command == "print-qemu-share-args":
        return print_qemu_share_args()

    if args.command == "serve":
        return serve()

    parser.error(f"unknown command: {args.command}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
