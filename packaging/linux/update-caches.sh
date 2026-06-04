#!/bin/sh
# Refresh the icon cache and desktop database so the Citeck Launcher menu entry
# and its icon appear immediately after install / upgrade / removal, without a
# re-login. Used as both the postinstall and postremove maintainer script for
# the .deb and .rpm. Best-effort — must never fail the package operation.
set -e

if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q /usr/share/applications || true
fi

exit 0
