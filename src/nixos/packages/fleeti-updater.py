#!/usr/bin/env python3

import json
import os
import shlex
import subprocess
import threading

import gi

gi.require_version("Gtk", "4.0")

from gi.repository import Gio, GLib, Gtk


SYSTEMD_SYSUPDATE = os.environ.get("FLEETI_SYSTEMD_SYSUPDATE", "systemd-sysupdate")
SYSTEMCTL = os.environ.get("FLEETI_SYSTEMCTL", "systemctl")
SUDO = os.environ.get("FLEETI_SUDO", "sudo")
OS_RELEASE_PATH = "/etc/os-release"


def run_command(args):
    env = os.environ.copy()
    env["SYSTEMD_COLORS"] = "0"
    env["SYSTEMD_PAGER"] = "cat"
    try:
        return subprocess.run(
            args, capture_output=True, text=True, env=env, check=False
        )
    except OSError as exc:
        return subprocess.CompletedProcess(args, 127, "", str(exc))


def run_privileged_command(args):
    return run_command([SUDO, "-n", *args])


def format_privileged_error(stderr, fallback):
    message = stderr.strip()
    if "password is required" in message or "a password is required" in message:
        return "Updater is not permitted to run privileged commands without a password."
    if "not allowed to execute" in message:
        return "Updater is not permitted to run this privileged command."
    return message or fallback


def read_os_release_field(name):
    prefix = name + "="
    try:
        with open(OS_RELEASE_PATH, encoding="utf-8") as os_release_file:
            for line in os_release_file:
                if not line.startswith(prefix):
                    continue

                raw_value = line[len(prefix) :].strip()
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


def is_sysupdate_status_output(stderr):
    message = stderr.strip()
    if not message:
        return False

    expected_prefixes = (
        "automatically discovered root block device",
        "discovering installed instances",
        "determining installed update sets",
        "newest installed version",
    )

    return all(
        line.lower().startswith(expected_prefixes)
        for line in message.splitlines()
    )


def get_privileged_command_failure(stderr):
    message = stderr.strip()
    if not message:
        return None

    lowered = message.lower()
    if (
        "password is required" in lowered
        or "a password is required" in lowered
        or "not allowed to execute" in lowered
        or "command not found" in lowered
        or "no such file or directory" in lowered
        or "sudo:" in lowered
    ):
        return format_privileged_error(message, "The privileged command failed.")

    return None


def parse_json_object(output):
    try:
        payload = json.loads(output)
    except json.JSONDecodeError:
        return None

    if not isinstance(payload, dict):
        return None

    return payload


def parse_check_new_response(output):
    payload = parse_json_object(output)
    if payload is None or "available" not in payload:
        return None

    available = payload["available"]
    if available is None:
        return {"available": None}

    if not isinstance(available, str):
        return None

    version = available.strip()
    return {"available": version or None}


def parse_release_details(output, fallback_version):
    payload = parse_json_object(output)
    if payload is None:
        return None

    version = payload.get("version")
    if not isinstance(version, str) or not version.strip():
        return None

    details = {
        "version": version.strip(),
        "changelog_urls": [],
    }

    changelog_urls = payload.get("changelogUrls")
    if isinstance(changelog_urls, list):
        details["changelog_urls"] = [
            url.strip()
            for url in changelog_urls
            if isinstance(url, str) and url.strip()
        ]

    return details


class FleetiUpdaterWindow(Gtk.ApplicationWindow):
    def __init__(self, app):
        super().__init__(application=app, title="Fleeti Updater")
        self.set_default_size(560, 360)

        self.available_version = None
        self.pending_update = False
        self.system_version = read_os_release_field("IMAGE_VERSION") or "unknown"

        root = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
        root.set_margin_top(24)
        root.set_margin_bottom(24)
        root.set_margin_start(24)
        root.set_margin_end(24)

        title = Gtk.Label()
        title.set_markup("<span size='x-large' weight='bold'>Fleeti Updater</span>")
        title.set_xalign(0)
        root.append(title)

        description = Gtk.Label(
            label=(
                "Check for new Fleeti releases, install the latest available system "
                "update, and reboot once the update is ready to use."
            )
        )
        description.set_wrap(True)
        description.set_xalign(0)
        root.append(description)

        self.version_label = Gtk.Label()
        self.version_label.set_xalign(0)
        root.append(self.version_label)
        self.update_version_label()

        self.status_label = Gtk.Label()
        self.status_label.set_wrap(True)
        self.status_label.set_xalign(0)
        root.append(self.status_label)

        self.details_expander = Gtk.Expander(label="Update details")
        self.details_expander.set_expanded(False)

        self.details_buffer = Gtk.TextBuffer()
        self.details_view = Gtk.TextView(buffer=self.details_buffer)
        self.details_view.set_editable(False)
        self.details_view.set_cursor_visible(False)
        self.details_view.set_monospace(True)
        self.details_view.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)

        self.details_scroller = Gtk.ScrolledWindow()
        self.details_scroller.set_policy(
            Gtk.PolicyType.AUTOMATIC,
            Gtk.PolicyType.AUTOMATIC,
        )
        self.details_scroller.set_min_content_height(180)
        self.details_scroller.set_propagate_natural_height(False)
        self.details_scroller.set_child(self.details_view)
        self.details_expander.set_child(self.details_scroller)
        root.append(self.details_expander)

        self.release_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.release_title = Gtk.Label()
        self.release_title.set_xalign(0)
        self.release_title.set_markup("<span weight='bold'>Update available</span>")
        self.release_box.append(self.release_title)

        self.release_label = Gtk.Label()
        self.release_label.set_wrap(True)
        self.release_label.set_xalign(0)
        self.release_box.append(self.release_label)

        self.changelog_label = Gtk.Label()
        self.changelog_label.set_wrap(True)
        self.changelog_label.set_selectable(True)
        self.changelog_label.set_xalign(0)
        self.release_box.append(self.changelog_label)
        root.append(self.release_box)

        self.spinner_row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=8)
        self.spinner = Gtk.Spinner()
        self.spinner_row.append(self.spinner)

        self.spinner_label = Gtk.Label()
        self.spinner_label.set_xalign(0)
        self.spinner_row.append(self.spinner_label)
        root.append(self.spinner_row)

        self.actions = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=12)

        self.check_button = Gtk.Button(label="Check for update")
        self.check_button.connect("clicked", self.on_check_clicked)
        self.actions.append(self.check_button)

        self.install_button = Gtk.Button(label="Install update")
        self.install_button.connect("clicked", self.on_install_clicked)
        self.actions.append(self.install_button)

        self.reboot_button = Gtk.Button(label="Reboot")
        self.reboot_button.connect("clicked", self.on_reboot_clicked)
        self.actions.append(self.reboot_button)

        root.append(self.actions)
        self.set_child(root)

        self.set_busy(None)
        self.show_idle()
        self.check_pending_update(is_startup=True)

    def set_busy(self, message):
        is_busy = bool(message)
        self.spinner_row.set_visible(is_busy)
        if is_busy:
            self.spinner_label.set_text(message)
            self.spinner.start()
            return

        self.spinner.stop()
        self.spinner_label.set_text("")

    def set_status(self, message):
        self.status_label.set_visible(bool(message))
        self.status_label.set_text(message or "")

    def hide_details(self):
        self.details_buffer.set_text("")
        self.details_expander.set_label("Update details")
        self.details_expander.set_expanded(False)
        self.details_expander.set_visible(False)

    def show_details(self, title, content):
        self.details_buffer.set_text(content)
        self.details_expander.set_label(title)
        self.details_expander.set_expanded(False)
        self.details_expander.set_visible(True)

    def format_command_details(self, result):
        sections = []

        stdout = result.stdout.strip()
        if stdout:
            sections.append("Standard output\n" + stdout)

        stderr = result.stderr.strip()
        if stderr:
            sections.append("Standard error\n" + stderr)

        return "\n\n".join(sections)

    def update_version_label(self):
        self.version_label.set_text(f"Fleeti system version: {self.system_version}")

    def hide_actions(self):
        self.check_button.set_visible(False)
        self.install_button.set_visible(False)
        self.reboot_button.set_visible(False)

    def hide_release(self):
        self.release_box.set_visible(False)
        self.release_label.set_text("")
        self.changelog_label.set_text("")
        self.changelog_label.set_visible(False)

    def show_idle(self):
        self.available_version = None
        self.hide_release()
        self.hide_details()
        self.hide_actions()
        self.check_button.set_visible(True)
        self.set_status(None)
        self.set_busy(None)

    def show_reboot_required(self, message):
        self.available_version = None
        self.hide_release()
        self.hide_details()
        self.hide_actions()
        self.reboot_button.set_visible(True)
        self.set_status(message)
        self.set_busy(None)

    def show_pending_update(self):
        self.show_reboot_required(
            "A previously installed Fleeti update is ready. Reboot to finish applying it."
        )

    def show_update_available(self, details):
        version = details["version"]
        self.available_version = version
        self.hide_details()
        self.hide_actions()
        self.install_button.set_visible(True)
        if self.pending_update:
            self.reboot_button.set_visible(True)
        self.release_box.set_visible(True)
        self.release_label.set_text(f"Release {version} is available to install.")

        changelog_urls = details.get("changelog_urls", [])
        if changelog_urls:
            changelog_text = "Change log: " + "\n".join(changelog_urls)
            self.changelog_label.set_text(changelog_text)
            self.changelog_label.set_visible(True)
        else:
            self.changelog_label.set_visible(False)

        if self.pending_update:
            self.set_status(
                "A newer Fleeti release is ready. Another installed update is still pending a reboot."
            )
        else:
            self.set_status("A newer Fleeti release is ready.")
        self.set_busy(None)

    def show_error(self, message):
        self.available_version = None
        self.hide_release()
        self.hide_details()
        self.hide_actions()
        self.check_button.set_visible(True)
        self.set_status(message)
        self.set_busy(None)

    def run_async(self, args, completion):
        def worker():
            result = run_privileged_command(args)
            GLib.idle_add(completion, result)

        thread = threading.Thread(target=worker, daemon=True)
        thread.start()

    def check_pending_update(self, is_startup):
        self.pending_update = False
        self.hide_actions()
        self.hide_release()
        self.hide_details()
        self.set_status("")
        self.set_busy("Checking if a reboot is already required...")
        self.run_async(
            [SYSTEMD_SYSUPDATE, "--no-pager", "pending"],
            lambda result: self.on_pending_checked(is_startup, result),
        )

    def on_pending_checked(self, is_startup, result):
        stderr = result.stderr.strip()
        if result.returncode == 0:
            self.pending_update = True
            self.check_for_updates(is_startup=is_startup)
            return False

        privileged_failure = get_privileged_command_failure(stderr)
        if privileged_failure is not None:
            self.show_error(
                f"Failed to check update state: {privileged_failure}"
            )
            return False

        if stderr and not is_sysupdate_status_output(stderr):
            self.show_error(
                f"Failed to check update state: {stderr}"
            )
            return False

        self.check_for_updates(is_startup=is_startup)
        return False

    def on_check_clicked(self, _button):
        self.check_pending_update(is_startup=False)

    def check_for_updates(self, is_startup):
        self.hide_actions()
        self.hide_release()
        self.hide_details()
        self.set_status("")
        self.set_busy("Checking for available updates...")
        self.run_async(
            [SYSTEMD_SYSUPDATE, "--json=short", "--no-pager", "check-new"],
            lambda result: self.on_check_finished(is_startup, result),
        )

    def on_check_finished(self, is_startup, result):
        stderr = result.stderr.strip()
        payload = parse_check_new_response(result.stdout)

        if payload is not None:
            available = payload["available"]
            if available:
                self.fetch_release_details(available)
                return False

            self.show_post_check_state(is_startup)
            return False

        privileged_failure = get_privileged_command_failure(stderr)
        if privileged_failure is not None:
            self.show_error(
                f"Failed to check for updates: {privileged_failure}"
            )
            return False

        if result.returncode != 0 and (not stderr or is_sysupdate_status_output(stderr)):
            self.show_post_check_state(is_startup)
            return False

        if stderr:
            self.show_error(f"Failed to check for updates: {stderr}")
            return False

        self.show_error(
            "Failed to check for updates: systemd-sysupdate returned invalid JSON."
        )
        return False

    def show_post_check_state(self, is_startup):
        if self.pending_update:
            self.show_pending_update()
            return

        self.show_idle()
        if not is_startup:
            self.set_status(
                "This system is already on the latest available Fleeti release."
            )

    def fetch_release_details(self, version):
        self.available_version = version
        self.set_busy(f"Loading details for release {version}...")
        self.run_async(
            [SYSTEMD_SYSUPDATE, "--json=short", "--no-pager", "list", version],
            lambda result: self.on_release_details_loaded(version, result),
        )

    def on_release_details_loaded(self, version, result):
        stderr = result.stderr.strip()
        if result.returncode != 0:
            if stderr:
                self.show_error(
                    f"Failed to load release details: {format_privileged_error(stderr, 'The release details check failed.')}"
                )
                return False

            self.show_error(
                "Failed to load release details: systemd-sysupdate did not return a valid response."
            )
            return False

        details = parse_release_details(result.stdout, version)
        if details is None:
            self.show_error(
                "Failed to load release details: systemd-sysupdate returned invalid JSON."
            )
            return False

        if details["version"] != version and version:
            self.show_error(
                "Failed to load release details: response version did not match the requested release."
            )
            return False

        self.show_update_available(details)
        return False

    def on_install_clicked(self, _button):
        self.hide_actions()
        self.hide_details()
        version = self.available_version
        if version:
            self.set_busy(f"Installing release {version}...")
        else:
            self.set_busy("Installing the latest available release...")

        self.set_status("")
        command = [SYSTEMD_SYSUPDATE, "--no-pager", "update"]
        if version:
            command.append(version)
        self.run_async(
            command,
            self.on_install_finished,
        )

    def on_install_finished(self, result):
        stderr = result.stderr.strip()
        details = self.format_command_details(result)
        if result.returncode != 0:
            message = format_privileged_error(stderr, "The update command failed.")
            self.show_error(f"Failed to install the update: {message}")
            if details:
                self.show_details("Update details", details)
            return False

        self.show_reboot_required(
            "The Fleeti update finished installing. Reboot to start the new release."
        )
        if details:
            self.show_details("Update details", details)
        return False

    def on_reboot_clicked(self, _button):
        self.hide_actions()
        self.hide_details()
        self.set_busy("Rebooting into the updated release...")
        self.set_status("")
        self.run_async([SYSTEMCTL, "reboot"], self.on_reboot_finished)

    def on_reboot_finished(self, result):
        stderr = result.stderr.strip()
        if result.returncode != 0:
            message = format_privileged_error(stderr, "The reboot command failed.")
            self.show_error(f"Failed to reboot the system: {message}")
            return False

        self.set_status("Reboot requested.")
        self.set_busy(None)
        return False


class FleetiUpdaterApplication(Gtk.Application):
    def __init__(self):
        super().__init__(
            application_id="ae.fleeti.Updater",
            flags=Gio.ApplicationFlags.DEFAULT_FLAGS,
        )

    def do_activate(self):
        window = self.props.active_window
        if window is None:
            window = FleetiUpdaterWindow(self)
        window.present()


def main():
    app = FleetiUpdaterApplication()
    return app.run(None)


if __name__ == "__main__":
    raise SystemExit(main())
