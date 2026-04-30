#!/usr/bin/env python3
# pyright: reportMissingImports=false

from __future__ import annotations

import argparse
import json
import os
import select
import socket
import sys
import termios
import tty
from typing import Any

import gi

gi.require_version("Gio", "2.0")

from gi.repository import Gio, GLib


BUS_NAME = "ae.fleeti.MoltHouse1"
OBJECT_PATH = "/ae/fleeti/MoltHouse1"
INTERFACE_NAME = "ae.fleeti.MoltHouse1"


def build_proxy(bus: str) -> Gio.DBusProxy:
    bus_type = Gio.BusType.SYSTEM
    if bus == "session":
        bus_type = Gio.BusType.SESSION

    return Gio.DBusProxy.new_for_bus_sync(
        bus_type,
        Gio.DBusProxyFlags.NONE,
        None,
        BUS_NAME,
        OBJECT_PATH,
        INTERFACE_NAME,
        None,
    )


def call_json(
    proxy: Gio.DBusProxy, method: str, parameters: GLib.Variant | None = None
) -> dict[str, Any]:
    if parameters is None:
        parameters = GLib.Variant("()", ())

    response = proxy.call_sync(
        method,
        parameters,
        Gio.DBusCallFlags.NONE,
        -1,
        None,
    )
    payload = response.unpack()[0]
    return json.loads(payload)


def print_payload(payload: Any) -> None:
    sys.stdout.write(json.dumps(payload, indent=2, sort_keys=True) + "\n")


def command_status(proxy: Gio.DBusProxy, _args: argparse.Namespace) -> int:
    payload = call_json(proxy, "GetState")
    print_payload(payload)
    return 0


def command_start(proxy: Gio.DBusProxy, _args: argparse.Namespace) -> int:
    payload = call_json(proxy, "StartVm")
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def command_stop(proxy: Gio.DBusProxy, _args: argparse.Namespace) -> int:
    payload = call_json(proxy, "StopVm")
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def command_restart(proxy: Gio.DBusProxy, _args: argparse.Namespace) -> int:
    payload = call_json(proxy, "RestartVm")
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def command_logs(proxy: Gio.DBusProxy, args: argparse.Namespace) -> int:
    payload = call_json(proxy, "GetRecentLogs", GLib.Variant("(i)", (args.lines,)))
    if args.json:
        print_payload(payload)
        return 0

    for line in payload.get("lines", []):
        sys.stdout.write(line + "\n")
    return 0


def bridge_console_socket(path: str) -> int:
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.connect(path)
    sock.sendall(b"\r")

    stdin_fd = sys.stdin.fileno()
    stdout_fd = sys.stdout.fileno()
    original_attrs = None
    if sys.stdin.isatty():
        original_attrs = termios.tcgetattr(stdin_fd)
        tty.setraw(stdin_fd)

    try:
        while True:
            ready, _, _ = select.select([stdin_fd, sock], [], [])
            if sock in ready:
                data = sock.recv(4096)
                if not data:
                    return 0
                os.write(stdout_fd, data)

            if stdin_fd in ready:
                data = os.read(stdin_fd, 4096)
                if not data:
                    try:
                        sock.shutdown(socket.SHUT_WR)
                    except OSError:
                        pass
                    continue
                sock.sendall(data)
    finally:
        sock.close()
        if original_attrs is not None:
            termios.tcsetattr(stdin_fd, termios.TCSADRAIN, original_attrs)


def command_console(proxy: Gio.DBusProxy, args: argparse.Namespace) -> int:
    payload = call_json(proxy, "GetConsoleState")
    if args.json:
        print_payload(payload)
        return 0 if payload.get("available", False) else 1

    if not payload.get("available", False):
        sys.stderr.write(
            f"molthousectl: {payload.get('message', 'console is unavailable')}\n"
        )
        return 1

    path = payload.get("path")
    if not isinstance(path, str) or path == "":
        sys.stderr.write("molthousectl: console path is missing\n")
        return 1

    try:
        return bridge_console_socket(path)
    except OSError as err:
        sys.stderr.write(f"molthousectl: failed to attach to console: {err}\n")
        return 1


def command_shares_list(proxy: Gio.DBusProxy, _args: argparse.Namespace) -> int:
    payload = call_json(proxy, "ListShares")
    print_payload(payload)
    return 0


def command_shares_add(proxy: Gio.DBusProxy, args: argparse.Namespace) -> int:
    payload = call_json(
        proxy,
        "AddShare",
        GLib.Variant("(ssb)", (args.source, args.mount_point, args.read_only)),
    )
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def command_shares_update(proxy: Gio.DBusProxy, args: argparse.Namespace) -> int:
    payload = call_json(
        proxy,
        "UpdateShare",
        GLib.Variant(
            "(sssb)",
            (args.share_id, args.source, args.mount_point, args.read_only),
        ),
    )
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def command_shares_remove(proxy: Gio.DBusProxy, args: argparse.Namespace) -> int:
    payload = call_json(proxy, "RemoveShare", GLib.Variant("(s)", (args.share_id,)))
    print_payload(payload)
    return 0 if payload.get("ok", False) else 1


def add_read_only_flags(parser: argparse.ArgumentParser) -> None:
    parser.set_defaults(read_only=True)
    group = parser.add_mutually_exclusive_group()
    group.add_argument(
        "--read-only",
        dest="read_only",
        action="store_true",
        help="Mount the share read-only (default)",
    )
    group.add_argument(
        "--read-write",
        dest="read_only",
        action="store_false",
        help="Mount the share read-write",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="MoltHouse control CLI")
    parser.add_argument(
        "--bus",
        choices=["system", "session"],
        default="system",
        help="D-Bus bus to use (default: system)",
    )

    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = True

    status_parser = subparsers.add_parser("status", help="Show MoltHouse runtime state")
    status_parser.set_defaults(handler=command_status)

    start_parser = subparsers.add_parser("start", help="Request VM start")
    start_parser.set_defaults(handler=command_start)

    stop_parser = subparsers.add_parser("stop", help="Request VM stop")
    stop_parser.set_defaults(handler=command_stop)

    restart_parser = subparsers.add_parser("restart", help="Request VM restart")
    restart_parser.set_defaults(handler=command_restart)

    logs_parser = subparsers.add_parser("logs", help="Show recent MoltHouse logs")
    logs_parser.add_argument(
        "--lines", type=int, default=50, help="Number of log lines to fetch"
    )
    logs_parser.add_argument(
        "--json", action="store_true", help="Print the full JSON payload"
    )
    logs_parser.set_defaults(handler=command_logs)

    console_parser = subparsers.add_parser(
        "console", help="Show console transport state"
    )
    console_parser.add_argument(
        "--json", action="store_true", help="Print console state instead of attaching"
    )
    console_parser.set_defaults(handler=command_console)

    shares_parser = subparsers.add_parser("shares", help="Manage MoltHouse shares")
    shares_subparsers = shares_parser.add_subparsers(dest="shares_command")
    shares_subparsers.required = True

    shares_list_parser = shares_subparsers.add_parser(
        "list", help="List configured shares"
    )
    shares_list_parser.set_defaults(handler=command_shares_list)

    shares_add_parser = shares_subparsers.add_parser("add", help="Add a share")
    shares_add_parser.add_argument("source", help="Host directory to share")
    shares_add_parser.add_argument("mount_point", help="Guest mount point")
    add_read_only_flags(shares_add_parser)
    shares_add_parser.set_defaults(handler=command_shares_add)

    shares_update_parser = shares_subparsers.add_parser("update", help="Update a share")
    shares_update_parser.add_argument("share_id", help="Share identifier")
    shares_update_parser.add_argument("source", help="Host directory to share")
    shares_update_parser.add_argument("mount_point", help="Guest mount point")
    add_read_only_flags(shares_update_parser)
    shares_update_parser.set_defaults(handler=command_shares_update)

    shares_remove_parser = shares_subparsers.add_parser("remove", help="Remove a share")
    shares_remove_parser.add_argument("share_id", help="Share identifier")
    shares_remove_parser.set_defaults(handler=command_shares_remove)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    try:
        proxy = build_proxy(args.bus)
        return args.handler(proxy, args)
    except GLib.Error as err:
        sys.stderr.write(f"molthousectl: D-Bus call failed: {err.message}\n")
        return 1
    except ConnectionError as err:
        sys.stderr.write(f"molthousectl: {err}\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
