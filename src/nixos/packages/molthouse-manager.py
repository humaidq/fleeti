#!/usr/bin/env python3
# pyright: reportMissingImports=false, reportUnknownVariableType=false, reportUnknownArgumentType=false, reportUnknownMemberType=false

from __future__ import annotations

import json
import os
import posixpath
from pathlib import Path
from pathlib import PurePosixPath
from typing import Any

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gio", "2.0")

from gi.repository import Gio, GLib, GObject, Gtk


def env_or_default(name: str, fallback: str) -> str:
    value = os.environ.get(name, "").strip()
    if value:
        return value
    return fallback


BUS_NAME = env_or_default("MOLTHOUSE_DBUS_NAME", "ae.fleeti.MoltHouse1")
OBJECT_PATH = env_or_default("MOLTHOUSE_DBUS_OBJECT_PATH", "/ae/fleeti/MoltHouse1")
INTERFACE_NAME = env_or_default("MOLTHOUSE_DBUS_INTERFACE", "ae.fleeti.MoltHouse1")
BUS_KIND = env_or_default("MOLTHOUSE_DBUS_BUS", "system")
VM_MAIN_USER_HOME = PurePosixPath("/home/fleeti")


def host_share_home() -> Path:
    return Path.home().resolve()


def normalize_host_share_source(source: str) -> Path:
    host_home = host_share_home()
    source = source.strip()
    if source == "":
        raise ValueError("Choose a host directory before saving the share")

    if source == "~":
        source_path = host_home
    elif source.startswith("~/"):
        source_path = host_home / source[2:]
    else:
        source_path = Path(source)

    if not source_path.is_absolute():
        raise ValueError(f"Choose a folder inside {host_home}")

    try:
        resolved = source_path.resolve(strict=True)
    except FileNotFoundError as err:
        raise ValueError(f"Host directory does not exist: {source_path}") from err

    if not resolved.is_dir():
        raise ValueError(f"Host directory must be a folder: {resolved}")

    try:
        relative = resolved.relative_to(host_home)
    except ValueError as err:
        raise ValueError(f"Choose a folder inside {host_home}") from err

    if len(relative.parts) == 0:
        raise ValueError(
            f"Choose a folder inside {host_home}, not the home folder itself"
        )

    return resolved


def guest_mount_point_for_source(source_path: Path) -> str:
    relative = source_path.relative_to(host_share_home())
    return str(VM_MAIN_USER_HOME.joinpath(*relative.parts))


def format_molthoused_error(message: str) -> str:
    message = message.strip()
    if message == "":
        return "Failed to contact molthoused."

    lowered = message.lower()
    if (
        "was not provided by any .service files" in lowered
        or "serviceunknown" in lowered
    ):
        return (
            "molthoused is not available on this system image yet. "
            "OpenClaw support only appears after building and deploying a profile with the runtime enabled."
        )

    return f"Failed to contact molthoused: {message}"


class LogEntryRow(GObject.Object):
    def __init__(self, timestamp: str, level: str, entry: str) -> None:
        super().__init__()
        self.timestamp = timestamp
        self.level = level
        self.entry = entry


def log_level_markup(level: str) -> str:
    escaped_level = GLib.markup_escape_text(level)
    normalized = level.upper()
    if normalized == "ERROR":
        color = "#c01c28"
    elif normalized in {"WARN", "WARNING"}:
        color = "#9a6700"
    elif normalized == "INFO":
        color = "#1a5fb4"
    else:
        color = "#5e5c64"
    return f"<span foreground='{color}' weight='bold'>{escaped_level}</span>"


def vm_status_markup(status: str) -> str:
    escaped_status = GLib.markup_escape_text(status.replace("_", " ").title())
    normalized = status.lower()
    if normalized == "running":
        color = "#2b7a0b"
    elif normalized == "starting":
        color = "#1a5fb4"
    elif normalized == "stopping":
        color = "#9a6700"
    elif normalized == "failed":
        color = "#c01c28"
    elif normalized == "stopped":
        color = "#5e5c64"
    else:
        color = "#6c6c6c"
    return f"<span foreground='{color}' weight='bold'>{escaped_status}</span>"


class MolthouseClient:
    def __init__(self, signal_handler) -> None:
        self.signal_handler = signal_handler
        self.connection: Any = None
        self.proxy: Any = None
        self.subscription_id = 0

    def _bus_type(self) -> Gio.BusType:
        if BUS_KIND == "session":
            return Gio.BusType.SESSION
        return Gio.BusType.SYSTEM

    def connect(self) -> None:
        if self.connection is not None and self.proxy is not None:
            return

        self.connection = Gio.bus_get_sync(self._bus_type(), None)
        self.proxy = Gio.DBusProxy.new_sync(
            self.connection,
            Gio.DBusProxyFlags.NONE,
            None,
            BUS_NAME,
            OBJECT_PATH,
            INTERFACE_NAME,
            None,
        )

        if self.subscription_id == 0:
            self.subscription_id = self.connection.signal_subscribe(
                BUS_NAME,
                INTERFACE_NAME,
                None,
                OBJECT_PATH,
                None,
                Gio.DBusSignalFlags.NONE,
                self._on_signal,
            )

    def _on_signal(
        self,
        _connection: Any,
        _sender_name: str,
        _object_path: str,
        _interface_name: str,
        signal_name: str,
        parameters: Any,
        *_args: Any,
    ) -> None:
        payload: dict[str, Any] = {}
        if parameters is not None and parameters.n_children() > 0:
            payload = json.loads(parameters.unpack()[0])
        self.signal_handler(signal_name, payload)

    def call_json(
        self, method: str, parameters: GLib.Variant | None = None
    ) -> dict[str, Any]:
        self.connect()
        if parameters is None:
            parameters = GLib.Variant("()", ())

        response = self.proxy.call_sync(
            method,
            parameters,
            Gio.DBusCallFlags.NONE,
            -1,
            None,
        )
        return json.loads(response.unpack()[0])

    def get_state(self) -> dict[str, Any]:
        return self.call_json("GetState")

    def start_vm(self) -> dict[str, Any]:
        return self.call_json("StartVm")

    def stop_vm(self) -> dict[str, Any]:
        return self.call_json("StopVm")

    def restart_vm(self) -> dict[str, Any]:
        return self.call_json("RestartVm")

    def list_shares(self) -> dict[str, Any]:
        return self.call_json("ListShares")

    def add_share(
        self, source: str, mount_point: str, read_only: bool
    ) -> dict[str, Any]:
        return self.call_json(
            "AddShare",
            GLib.Variant("(ssb)", (source, mount_point, read_only)),
        )

    def update_share(
        self, share_id: str, source: str, mount_point: str, read_only: bool
    ) -> dict[str, Any]:
        return self.call_json(
            "UpdateShare",
            GLib.Variant("(sssb)", (share_id, source, mount_point, read_only)),
        )

    def remove_share(self, share_id: str) -> dict[str, Any]:
        return self.call_json("RemoveShare", GLib.Variant("(s)", (share_id,)))

    def get_recent_logs(self, lines: int) -> dict[str, Any]:
        return self.call_json("GetRecentLogs", GLib.Variant("(i)", (lines,)))

    def get_console_state(self) -> dict[str, Any]:
        return self.call_json("GetConsoleState")


class MolthouseWindow(Gtk.ApplicationWindow):
    def __init__(self, app: Gtk.Application):
        super().__init__(application=app, title="MoltHouse Manager")
        self.set_default_size(1080, 760)

        self.client = MolthouseClient(self.on_signal)
        self.state_payload: dict[str, Any] = {}
        self.shares_payload: dict[str, Any] = {"shares": []}
        self.logs_payload: dict[str, Any] = {"lines": []}
        self.console_payload: dict[str, Any] = {}
        self.selected_share_id: str | None = None

        root = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=18)
        root.set_margin_top(20)
        root.set_margin_bottom(20)
        root.set_margin_start(20)
        root.set_margin_end(20)

        heading = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        title = Gtk.Label()
        title.set_markup("<span size='x-large' weight='bold'>MoltHouse Manager</span>")
        title.set_xalign(0)

        subtitle = Gtk.Label(
            label=(
                "A sandboxed runtime for OpenClaw."
            )
        )
        subtitle.set_wrap(True)
        subtitle.set_xalign(0)

        self.feedback_label = Gtk.Label()
        self.feedback_label.set_wrap(True)
        self.feedback_label.set_xalign(0)
        self.feedback_label.set_visible(False)

        heading.append(title)
        heading.append(subtitle)
        heading.append(self.feedback_label)
        root.append(heading)

        stack = Gtk.Stack()
        stack.set_hexpand(True)
        stack.set_vexpand(True)
        stack.set_transition_type(Gtk.StackTransitionType.CROSSFADE)

        sidebar = Gtk.StackSidebar()
        sidebar.set_stack(stack)
        sidebar.set_vexpand(True)
        sidebar.set_size_request(220, -1)

        stack.add_titled(self.build_overview_page(), "overview", "Overview")
        stack.add_titled(self.build_shares_page(), "shares", "Shares")
        stack.add_titled(self.build_logs_page(), "logs", "Logs")

        content = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=18)
        content.set_hexpand(True)
        content.set_vexpand(True)
        content.append(sidebar)
        content.append(Gtk.Separator(orientation=Gtk.Orientation.VERTICAL))
        content.append(stack)
        root.append(content)

        self.set_child(root)
        self.refresh_all(show_success=False)

    def set_feedback(self, message: str, *, error: bool = False) -> None:
        if message == "":
            self.feedback_label.set_visible(False)
            self.feedback_label.set_text("")
            return

        prefix = "Error" if error else "Info"
        self.feedback_label.set_text(f"{prefix}: {message}")
        self.feedback_label.set_visible(True)

    def clear_feedback(self) -> None:
        self.set_feedback("")

    def console_socket_exists(self) -> bool:
        path = self.console_payload.get("path")
        if not isinstance(path, str) or path == "":
            return False
        return Path(path).exists()

    def on_signal(self, signal_name: str, payload: dict[str, Any]) -> None:
        if signal_name == "StateChanged":
            self.state_payload = payload
        elif signal_name == "SharesChanged":
            self.shares_payload = payload
        elif signal_name == "VmFailed":
            message = payload.get("last_error", "MoltHouse reported a VM failure")
            self.set_feedback(message, error=True)

        GLib.idle_add(self.refresh_all, False)

    def call_action(self, action, *, success_message: str | None = None) -> None:
        try:
            payload = action()
        except GLib.Error as err:
            self.set_feedback(err.message, error=True)
            self.refresh_all(show_success=False)
            return

        if payload.get("ok", False):
            if success_message:
                self.set_feedback(success_message)
        else:
            self.set_feedback(payload.get("message", "Operation failed"), error=True)

        self.refresh_all(show_success=False)

    def refresh_all(self, show_success: bool = False) -> bool:
        try:
            self.state_payload = self.client.get_state()
            self.shares_payload = self.client.list_shares()
            self.logs_payload = self.client.get_recent_logs(100)
            self.console_payload = self.client.get_console_state()
            if show_success:
                self.set_feedback("Refreshed MoltHouse state")
        except GLib.Error as err:
            self.set_feedback(format_molthoused_error(err.message), error=True)

        self.update_overview_page()
        self.update_shares_page()
        self.update_logs_page()
        return False

    def section_heading(self, text: str) -> Gtk.Label:
        label = Gtk.Label()
        label.set_markup(f"<span weight='bold'>{text}</span>")
        label.set_xalign(0)
        return label

    def build_page_shell(
        self, title: str, description: str, content: Gtk.Widget
    ) -> Gtk.Widget:
        page = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
        page.set_margin_top(8)
        page.set_margin_bottom(8)
        page.set_margin_start(8)
        page.set_margin_end(8)

        heading = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=4)
        title_label = Gtk.Label()
        title_label.set_markup(f"<span size='large' weight='bold'>{title}</span>")
        title_label.set_xalign(0)

        description_label = Gtk.Label(label=description)
        description_label.set_wrap(True)
        description_label.set_xalign(0)

        heading.append(title_label)
        heading.append(description_label)

        page.append(heading)
        page.append(Gtk.Separator(orientation=Gtk.Orientation.HORIZONTAL))
        page.append(content)
        return page

    def parse_log_row(self, line: str) -> LogEntryRow:
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            return LogEntryRow("", "", line)

        timestamp = str(record.get("timestamp", ""))
        level = str(record.get("level", ""))
        message = str(record.get("message", line))
        details = record.get("details")
        if details not in (None, {}, []):
            message = f"{message}\n{json.dumps(details, sort_keys=True)}"
        return LogEntryRow(timestamp, level, message)

    def build_log_column(
        self,
        title: str,
        value_getter,
        *,
        expand: bool = False,
        monospace: bool = False,
        markup: bool = False,
    ) -> Gtk.ColumnViewColumn:
        factory = Gtk.SignalListItemFactory()
        factory.connect("setup", self.on_log_column_setup, monospace)
        factory.connect("bind", self.on_log_column_bind, value_getter, markup)

        column = Gtk.ColumnViewColumn.new(title, factory)
        column.set_expand(expand)
        column.set_resizable(True)
        return column

    def on_log_column_setup(
        self,
        _factory: Gtk.SignalListItemFactory,
        list_item: Gtk.ListItem,
        monospace: bool,
    ) -> None:
        label = Gtk.Label(xalign=0)
        label.set_wrap(True)
        label.set_selectable(True)
        if monospace:
            label.add_css_class("monospace")
        list_item.set_child(label)

    def on_log_column_bind(
        self,
        _factory: Gtk.SignalListItemFactory,
        list_item: Gtk.ListItem,
        value_getter,
        markup: bool,
    ) -> None:
        item = list_item.get_item()
        child = list_item.get_child()
        if item is None or child is None:
            return
        value = value_getter(item)
        if markup:
            child.set_markup(value)
            return
        child.set_text(value)

    def clear_listbox(self, listbox: Gtk.ListBox) -> None:
        child = listbox.get_first_child()
        while child is not None:
            next_child = child.get_next_sibling()
            listbox.remove(child)
            child = next_child

    def build_overview_page(self) -> Gtk.Widget:
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)

        actions = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
        self.refresh_button = Gtk.Button(label="Refresh")
        self.refresh_button.connect(
            "clicked", lambda _button: self.refresh_all(show_success=True)
        )
        self.start_button = Gtk.Button(label="Start")
        self.start_button.connect(
            "clicked", lambda _button: self.call_action(self.client.start_vm)
        )
        self.stop_button = Gtk.Button(label="Stop")
        self.stop_button.connect(
            "clicked", lambda _button: self.call_action(self.client.stop_vm)
        )
        self.restart_button = Gtk.Button(label="Restart")
        self.restart_button.connect(
            "clicked", lambda _button: self.call_action(self.client.restart_vm)
        )
        self.open_console_button = Gtk.Button(label="Open VM Console")
        self.open_console_button.connect("clicked", self.on_open_vm_console)

        actions.append(self.refresh_button)
        actions.append(self.start_button)
        actions.append(self.stop_button)
        actions.append(self.restart_button)
        actions.append(self.open_console_button)
        box.append(actions)

        summary = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        summary.append(self.section_heading("Current State"))
        self.overview_status_label = Gtk.Label(xalign=0)
        self.overview_status_label.set_use_markup(True)
        self.overview_vm_name_label = Gtk.Label(xalign=0)
        self.overview_boot_ready_label = Gtk.Label(xalign=0)
        self.overview_shares_count_label = Gtk.Label(xalign=0)
        self.overview_last_error_label = Gtk.Label(xalign=0)
        self.overview_last_error_label.set_wrap(True)

        summary.append(self.overview_status_label)
        summary.append(self.overview_vm_name_label)
        summary.append(self.overview_boot_ready_label)
        summary.append(self.overview_shares_count_label)
        summary.append(self.overview_last_error_label)
        self.overview_restart_required_label = Gtk.Label(xalign=0)
        self.overview_restart_required_label.set_wrap(True)
        summary.append(self.overview_restart_required_label)
        box.append(summary)

        self.overview_boot_blockers_label = Gtk.Label(xalign=0)
        self.overview_boot_blockers_label.set_wrap(True)
        self.overview_boot_blockers_label.set_selectable(True)
        self.overview_boot_blockers_label.set_visible(False)
        box.append(self.overview_boot_blockers_label)

        self.overview_details_label = Gtk.Label(xalign=0)
        self.overview_details_label.set_wrap(True)
        self.overview_details_label.set_selectable(True)

        details_expander = Gtk.Expander(label="Runtime Paths and Sockets")
        details_expander.set_expanded(False)
        details_expander.set_child(self.overview_details_label)
        box.append(details_expander)
        return self.build_page_shell(
            "Overview",
            "Inspect the VM state and run common MoltHouse actions.",
            box,
        )

    def update_overview_page(self) -> None:
        state = self.state_payload
        vm = state.get("vm", {})
        paths = state.get("paths", {})

        status = state.get("status", "unknown")
        self.overview_status_label.set_markup(
            f"Status: {vm_status_markup(str(status))}"
        )
        self.overview_vm_name_label.set_text(f"VM name: {vm.get('name', 'unknown')}")
        self.overview_boot_ready_label.set_text(
            f"Boot ready: {'yes' if vm.get('boot_ready', False) else 'no'}"
        )
        self.overview_shares_count_label.set_text(
            f"Configured shares: {vm.get('shares_count', 0)} (applied: {vm.get('applied_shares_count', 0)})"
        )

        last_error = state.get("last_error") or "No active error"
        self.overview_last_error_label.set_text(f"Last error: {last_error}")

        restart_required = vm.get("restart_required", False)
        if restart_required:
            self.overview_restart_required_label.set_text(
                "Share configuration changed while the VM is running. Restart the VM to apply those share changes."
            )
        elif status == "running" and vm.get("shares_count", 0) != vm.get(
            "applied_shares_count", 0
        ):
            self.overview_restart_required_label.set_text(
                "One or more boot-time shares are still pending or failed. Restart the VM after fixing the issue to reapply them."
            )
        else:
            self.overview_restart_required_label.set_text(
                "Share changes are applied when the VM starts. Restart the VM after editing shares."
            )

        self.open_console_button.set_sensitive(self.console_socket_exists())

        console_path = self.console_payload.get("path", "unknown")
        console_message = self.console_payload.get("message", "Console state unknown")

        details = [
            f"Config: {paths.get('config', 'unknown')}",
            f"Launch plan: {paths.get('launch_plan', 'unknown')}",
            f"VM runtime: {paths.get('vm_runtime', 'unknown')}",
            f"QMP socket: {paths.get('qmp_socket', 'unknown')}",
            f"Console socket: {console_path}",
            f"Console state: {console_message}",
            f"Writable disk: {paths.get('writable_disk', 'unknown')}",
        ]
        boot_blockers = vm.get("boot_blockers", [])
        if boot_blockers:
            blockers_text = "Boot blockers:\n" + "\n".join(
                f"- {blocker}" for blocker in boot_blockers
            )
            self.overview_boot_blockers_label.set_text(blockers_text)
            self.overview_boot_blockers_label.set_visible(True)
        else:
            self.overview_boot_blockers_label.set_text("")
            self.overview_boot_blockers_label.set_visible(False)
        self.overview_details_label.set_text("\n".join(details))

    def on_open_vm_console(self, _button: Gtk.Button) -> None:
        if not self.console_socket_exists():
            self.set_feedback(
                self.console_payload.get(
                    "message", "Console transport is unavailable."
                ),
                error=True,
            )
            return

        try:
            Gio.Subprocess.new(
                [
                    "foot",
                    "-T",
                    "MoltHouse VM Console",
                    "molthousectl",
                    "--bus",
                    BUS_KIND,
                    "console",
                ],
                Gio.SubprocessFlags.SEARCH_PATH_FROM_ENVP,
            )
        except GLib.Error as err:
            self.set_feedback(f"Failed to open the VM console: {err.message}", error=True)
            return

        self.set_feedback("Opened the MoltHouse VM console.")

    def build_shares_page(self) -> Gtk.Widget:
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)

        notice = Gtk.Label(
            label=(
                "Pick a folder inside your home directory. MoltHouse mounts it at the same relative path under the VM user's home."
            )
        )
        notice.set_wrap(True)
        notice.set_xalign(0)
        box.append(notice)
        self.shares_notice_label = notice

        actions = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
        refresh_button = Gtk.Button(label="Refresh shares")
        refresh_button.connect(
            "clicked", lambda _button: self.refresh_all(show_success=True)
        )
        actions.append(refresh_button)
        box.append(actions)

        scroller = Gtk.ScrolledWindow()
        scroller.set_vexpand(True)
        self.shares_listbox = Gtk.ListBox()
        self.shares_listbox.set_selection_mode(Gtk.SelectionMode.SINGLE)
        self.shares_listbox.connect("row-selected", self.on_share_selected)
        scroller.set_child(self.shares_listbox)
        box.append(scroller)

        form = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
        form.append(self.section_heading("Add or Edit Share"))

        self.share_source_entry = Gtk.Entry()
        self.share_source_entry.set_placeholder_text("Host directory inside your home folder")
        self.share_source_entry.connect("changed", self.on_share_source_changed)
        self.share_mount_entry = Gtk.Entry()
        self.share_mount_entry.set_placeholder_text("VM path, derived automatically")
        self.share_mount_entry.set_editable(False)
        self.share_read_only_check = Gtk.CheckButton(label="Read only")
        self.share_read_only_check.set_active(True)
        self.share_form_status = Gtk.Label(xalign=0)
        self.share_form_status.set_wrap(True)

        mount_hint = Gtk.Label(
            label="Guest path: mounted under /home/fleeti using the same relative path.",
            xalign=0,
        )
        mount_hint.set_wrap(True)

        form.append(self.share_source_entry)
        form.append(self.share_mount_entry)
        form.append(mount_hint)
        form.append(self.share_read_only_check)

        chooser_row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=6)
        choose_button = Gtk.Button(label="Choose directory")
        choose_button.connect("clicked", self.on_choose_directory)
        save_button = Gtk.Button(label="Save share")
        save_button.connect("clicked", self.on_save_share)
        remove_button = Gtk.Button(label="Remove selected")
        remove_button.connect("clicked", self.on_remove_share)
        clear_button = Gtk.Button(label="Clear selection")
        clear_button.connect("clicked", lambda _button: self.clear_share_form())
        chooser_row.append(choose_button)
        chooser_row.append(save_button)
        chooser_row.append(remove_button)
        chooser_row.append(clear_button)

        form.append(chooser_row)
        form.append(self.share_form_status)
        box.append(form)
        return self.build_page_shell(
            "Shares",
            "Manage host folders that MoltHouse exposes inside the OpenClaw VM.",
            box,
        )

    def update_shares_page(self) -> None:
        shares = self.shares_payload.get("shares", [])
        vm = self.state_payload.get("vm", {})
        state_status = self.state_payload.get("status", "unknown")
        failed_shares = [share for share in shares if share.get("status") == "failed"]
        if failed_shares:
            self.shares_notice_label.set_text(
                "One or more boot-time shares failed to mount. Review the status shown for each share below, then restart the VM after fixing the issue."
            )
        elif state_status == "running":
            self.shares_notice_label.set_text(
                "Share changes are saved immediately, but the running VM keeps its current share set until you restart it."
            )
        else:
            self.shares_notice_label.set_text(
                "Share changes are saved immediately in MoltHouse and will be mounted the next time the VM starts."
            )

        self.clear_listbox(self.shares_listbox)

        for share in shares:
            row = Gtk.ListBoxRow()
            row.share_id = share["id"]

            content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=4)
            title = Gtk.Label(xalign=0)
            mode = "read-only" if share.get("read_only", True) else "read-write"
            status = str(share.get("status", "configured"))
            title.set_markup(
                f"<span weight='bold'>{share['id']}</span> <span foreground='gray'>({mode}, {status})</span>"
            )
            detail_lines = [
                f"Host: {share['source']}",
                f"Guest: {share['mount_point']}",
            ]
            last_error = share.get("last_error")
            if isinstance(last_error, str) and last_error != "":
                detail_lines.append(f"Error: {last_error}")
            details = Gtk.Label(label="\n".join(detail_lines), xalign=0)
            details.set_wrap(True)

            content.append(title)
            content.append(details)
            row.set_child(content)
            self.shares_listbox.append(row)

        if self.selected_share_id is not None:
            for share in shares:
                if share["id"] == self.selected_share_id:
                    self.populate_share_form(share)
                    break
            else:
                self.clear_share_form()

    def populate_share_form(self, share: dict[str, Any]) -> None:
        self.selected_share_id = share["id"]
        self.share_source_entry.set_text(share["source"])
        self.update_share_mount_preview(fallback=share["mount_point"])
        self.share_read_only_check.set_active(share.get("read_only", True))
        self.share_form_status.set_text(f"Editing share: {self.selected_share_id}")

    def clear_share_form(self) -> None:
        self.selected_share_id = None
        self.share_source_entry.set_text("")
        self.share_mount_entry.set_text("")
        self.share_read_only_check.set_active(True)
        self.share_form_status.set_text("Creating a new share")
        self.shares_listbox.unselect_all()

    def on_share_selected(
        self, _listbox: Gtk.ListBox, row: Gtk.ListBoxRow | None
    ) -> None:
        if row is None:
            return

        for share in self.shares_payload.get("shares", []):
            if share["id"] == getattr(row, "share_id", None):
                self.populate_share_form(share)
                return

    def on_choose_directory(self, _button: Gtk.Button) -> None:
        chooser = Gtk.FileChooserNative(
            title="Select host directory",
            transient_for=self,
            action=Gtk.FileChooserAction.SELECT_FOLDER,
            accept_label="Select",
            cancel_label="Cancel",
        )
        chooser.connect("response", self.on_directory_chosen)
        chooser.show()

    def on_directory_chosen(
        self, chooser: Gtk.FileChooserNative, response: int
    ) -> None:
        if response == Gtk.ResponseType.ACCEPT:
            file = chooser.get_file()
            if file is not None:
                path = file.get_path()
                if path:
                    self.share_source_entry.set_text(path)
        chooser.destroy()

    def on_share_source_changed(self, _entry: Gtk.Entry) -> None:
        self.update_share_mount_preview()

    def update_share_mount_preview(self, fallback: str = "") -> None:
        source = self.share_source_entry.get_text().strip()
        if source == "":
            self.share_mount_entry.set_text("")
            return

        try:
            source_path = normalize_host_share_source(source)
        except ValueError:
            self.share_mount_entry.set_text(fallback)
            return

        self.share_mount_entry.set_text(guest_mount_point_for_source(source_path))

    def validate_share_inputs(self) -> tuple[str, str] | None:
        source = self.share_source_entry.get_text().strip()

        try:
            source_path = normalize_host_share_source(source)
        except ValueError as err:
            self.set_feedback(str(err), error=True)
            return None

        normalized_mount_point = posixpath.normpath(
            guest_mount_point_for_source(source_path)
        )
        self.share_mount_entry.set_text(normalized_mount_point)
        return str(source_path), normalized_mount_point

    def on_save_share(self, _button: Gtk.Button) -> None:
        validated = self.validate_share_inputs()
        if validated is None:
            return

        source, mount_point = validated
        read_only = self.share_read_only_check.get_active()

        try:
            if self.selected_share_id is None:
                payload = self.client.add_share(source, mount_point, read_only)
            else:
                payload = self.client.update_share(
                    self.selected_share_id,
                    source,
                    mount_point,
                    read_only,
                )
        except GLib.Error as err:
            self.set_feedback(err.message, error=True)
            return

        if payload.get("ok", False):
            state = payload.get("state", {})
            status = state.get("status") if isinstance(state, dict) else None
            if status == "running":
                self.set_feedback(
                    "Share saved and applied live."
                )
            else:
                self.set_feedback("Share saved. It will be mounted on next VM start.")
            self.clear_share_form()
        else:
            self.set_feedback(
                payload.get("message", "Failed to save share"), error=True
            )

        self.refresh_all(show_success=False)

    def on_remove_share(self, _button: Gtk.Button) -> None:
        if self.selected_share_id is None:
            self.set_feedback("Select a share before removing it", error=True)
            return

        try:
            payload = self.client.remove_share(self.selected_share_id)
        except GLib.Error as err:
            self.set_feedback(err.message, error=True)
            return

        if payload.get("ok", False):
            state = payload.get("state", {})
            status = state.get("status") if isinstance(state, dict) else None
            if status == "running":
                self.set_feedback(
                    "Share removed live from the running VM."
                )
            else:
                self.set_feedback("Share removed.")
            self.clear_share_form()
        else:
            self.set_feedback(
                payload.get("message", "Failed to remove share"), error=True
            )

        self.refresh_all(show_success=False)

    def build_logs_page(self) -> Gtk.Widget:
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)

        refresh_button = Gtk.Button(label="Refresh logs")
        refresh_button.connect(
            "clicked", lambda _button: self.refresh_all(show_success=True)
        )
        box.append(refresh_button)

        self.logs_error_label = Gtk.Label(xalign=0)
        self.logs_error_label.set_wrap(True)
        box.append(self.logs_error_label)

        scroller = Gtk.ScrolledWindow()
        scroller.set_vexpand(True)
        self.logs_store = Gio.ListStore.new(LogEntryRow)
        self.logs_view = Gtk.ColumnView()
        self.logs_view.set_model(Gtk.NoSelection.new(self.logs_store))
        self.logs_view.set_show_row_separators(True)
        self.logs_view.append_column(
            self.build_log_column("Timestamp", lambda row: row.timestamp)
        )
        self.logs_view.append_column(
            self.build_log_column(
                "Level", lambda row: log_level_markup(row.level), markup=True
            )
        )
        self.logs_view.append_column(
            self.build_log_column(
                "Entry", lambda row: row.entry, expand=True, monospace=True
            )
        )
        scroller.set_child(self.logs_view)
        box.append(scroller)
        return self.build_page_shell(
            "Logs",
            "Review recent MoltHouse helper output and VM-related errors.",
            box,
        )

    def update_logs_page(self) -> None:
        last_error = self.state_payload.get("last_error") or "No recent helper error"
        self.logs_error_label.set_text(f"Recent helper/VM error: {last_error}")

        lines = self.logs_payload.get("lines", [])
        self.logs_store.remove_all()
        for line in reversed(lines):
            if not isinstance(line, str):
                continue
            self.logs_store.append(self.parse_log_row(line))


class MolthouseManagerApplication(Gtk.Application):
    def __init__(self):
        super().__init__(
            application_id="ae.fleeti.MoltHouseManager",
            flags=Gio.ApplicationFlags.DEFAULT_FLAGS,
        )

    def do_activate(self) -> None:
        window = self.props.active_window
        if window is None:
            window = MolthouseWindow(self)
        window.present()


def main() -> int:
    app = MolthouseManagerApplication()
    return app.run(None)


if __name__ == "__main__":
    raise SystemExit(main())
