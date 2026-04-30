#!/usr/bin/env python3

from __future__ import annotations

import json
import os
import pwd
import socket
import subprocess
from pathlib import Path
from typing import Any


PORT = int(os.environ.get("OPENCLAW_MOUNTD_PORT", "10770"))
GUEST_USER = os.environ.get("OPENCLAW_MOUNTD_USER", "fleeti").strip() or "fleeti"


def run_command(command: list[str]) -> None:
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode == 0:
        return

    message = result.stderr.strip() or result.stdout.strip()
    if message == "":
        message = f"{' '.join(command)} failed with exit code {result.returncode}"
    raise RuntimeError(message)


def read_mounts() -> list[dict[str, str]]:
    mounts: list[dict[str, str]] = []
    with Path("/proc/self/mounts").open(encoding="utf-8") as handle:
        for line in handle:
            fields = line.split()
            if len(fields) < 4:
                continue
            mounts.append(
                {
                    "source": fields[0],
                    "target": fields[1].replace("\\040", " "),
                    "fstype": fields[2],
                    "options": fields[3],
                }
            )
    return mounts


def find_mount(mount_point: str) -> dict[str, str] | None:
    for mount in read_mounts():
        if mount["target"] == mount_point:
            return mount
    return None


def guest_user_details() -> tuple[int, int, Path]:
    try:
        user = pwd.getpwnam(GUEST_USER)
    except KeyError as err:
        raise RuntimeError(f"guest user does not exist: {GUEST_USER}") from err
    return user.pw_uid, user.pw_gid, Path(user.pw_dir)


def ensure_guest_mount_path(mount_point: str) -> None:
    uid, gid, guest_home = guest_user_details()
    mount_path = Path(mount_point)

    if not mount_path.is_absolute():
        raise RuntimeError(f"mount point must be absolute: {mount_point}")

    try:
        relative = mount_path.relative_to(guest_home)
    except ValueError as err:
        raise RuntimeError(f"mount point must stay under {guest_home}") from err

    if len(relative.parts) == 0:
        raise RuntimeError(f"mount point must not be the guest home itself: {guest_home}")

    current = guest_home
    for part in relative.parts:
        current = current / part
        if current.exists():
            if current.is_symlink():
                raise RuntimeError(f"mount path must not traverse symlinks: {current}")
            if not current.is_dir():
                raise RuntimeError(f"mount path component is not a directory: {current}")
        else:
            current.mkdir(mode=0o755)

        stat_result = current.stat()
        if stat_result.st_uid == 0 and stat_result.st_gid == 0:
            os.chown(current, uid, gid)


def handle_mount(request: dict[str, Any]) -> dict[str, Any]:
    tag = str(request.get("tag", "")).strip()
    mount_point = str(request.get("mount_point", "")).strip()
    read_only = bool(request.get("read_only", True))

    if tag == "" or mount_point == "":
        raise RuntimeError("mount requests require both tag and mount_point")

    existing = find_mount(mount_point)
    if existing is not None:
        if existing["fstype"] == "virtiofs" and existing["source"] == tag:
            return {
                "ok": True,
                "message": "Share is already mounted.",
                "mount_point": mount_point,
                "tag": tag,
            }
        raise RuntimeError(
            f"mount point is already in use: {mount_point} ({existing['source']} on {existing['fstype']})"
        )

    ensure_guest_mount_path(mount_point)
    options = ["defaults"]
    if read_only:
        options.append("ro")

    run_command(
        [
            "/run/current-system/sw/bin/mount",
            "-t",
            "virtiofs",
            tag,
            mount_point,
            "-o",
            ",".join(options),
        ]
    )
    return {
        "ok": True,
        "message": "Share mounted.",
        "mount_point": mount_point,
        "tag": tag,
    }


def handle_unmount(request: dict[str, Any]) -> dict[str, Any]:
    mount_point = str(request.get("mount_point", "")).strip()
    if mount_point == "":
        raise RuntimeError("unmount requests require mount_point")

    existing = find_mount(mount_point)
    if existing is None:
        return {
            "ok": True,
            "message": "Share is already unmounted.",
            "mount_point": mount_point,
        }

    run_command(["/run/current-system/sw/bin/umount", mount_point])
    return {
        "ok": True,
        "message": "Share unmounted.",
        "mount_point": mount_point,
    }


def handle_request(request: dict[str, Any]) -> dict[str, Any]:
    action = str(request.get("action", "")).strip()
    if action == "mount":
        return handle_mount(request)
    if action == "unmount":
        return handle_unmount(request)
    raise RuntimeError(f"unknown action: {action}")


def serve() -> int:
    server = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((socket.VMADDR_CID_ANY, PORT))
    server.listen()

    while True:
        connection, _address = server.accept()
        with connection:
            reader = connection.makefile("r", encoding="utf-8")
            writer = connection.makefile("w", encoding="utf-8")
            try:
                line = reader.readline()
                if line == "":
                    continue
                request = json.loads(line)
                response = handle_request(request)
            except Exception as err:
                response = {
                    "ok": False,
                    "message": str(err),
                }

            writer.write(json.dumps(response, sort_keys=True) + "\n")
            writer.flush()


if __name__ == "__main__":
    raise SystemExit(serve())
