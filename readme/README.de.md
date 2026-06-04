![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · **Deutsch** · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**Ihre gesamte Citeck-Plattform — einsatzbereit mit einem einzigen Befehl.**

Citeck Launcher ist ein selbst gehosteter Installer und Container-Manager für die Low-Code-BPM/ECM-Plattform **Citeck**. Es handelt sich um eine einzige, etwa 24 MB große Binärdatei, die als Kommandozeilen-Tool, als Hintergrund-Daemon und als plattformübergreifende Desktop-App funktioniert, jeden Citeck-Dienst als Docker-Container ausführt und sie in isolierten Namespaces gruppiert.

[Citeck](https://github.com/Citeck) ist eine quelloffene Low-Code-Plattform für Enterprise Content Management (ECM) und Business Process Management (BPM). Der Launcher installiert, aktualisiert und betreibt einen vollständigen Citeck-Stack — Keycloak, PostgreSQL, RabbitMQ und die Citeck-Webanwendungen — auf einem einzigen Host mit einem einzigen Befehl.

> **Vollständige Dokumentation:** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html

## Schnellstart

Voraussetzungen: Docker (läuft).

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Das Installationsskript lädt die neueste Version für Ihre Plattform herunter und installiert sie nach `/usr/local/bin/`. Der Assistent richtet den Namespace ein und startet die Plattform.

> **Wichtig:** Der Befehl `citeck install` ist ein **interaktiver TUI-Assistent** und erfordert ein echtes Terminal. Der Assistent gibt das generierte Admin-Passwort am Ende **einmalig** aus — kopieren und speichern Sie es unbedingt, da Sie es nach dem Schließen des Bildschirms nicht wiederherstellen können. Falls Sie es verlieren, setzen Sie es über `citeck setup admin-password` zurück (siehe die [Befehlsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)). Durch Drücken von `Ctrl+C` vor dem letzten Schritt „Konfiguration schreiben“ wird der Vorgang ohne Änderungen beendet; wird er später unterbrochen, prüfen Sie `/opt/citeck/conf/` auf unvollständige Zustände.
>
> Eine automatisierte / nicht-interaktive Installation ist eine künftige Funktion — bitte erstellen Sie ein Issue, falls Sie diese benötigen.

Um eine bestehende Installation zu **aktualisieren**, führen Sie denselben Einzeiler aus — das Skript erkennt die installierte Version, fordert zur Aktualisierung auf, stoppt den Daemon und ersetzt die Binärdatei (ein Backup wird unter `/usr/local/bin/citeck.bak` aufbewahrt und kann über `citeck install --rollback` wiederhergestellt werden).

### Offline-Installation

Für Server ohne Internetzugang laden Sie sowohl die Binärdatei als auch das
Workspace-Archiv vorab herunter:

1. **Binärdatei:** von der [Releases-Seite](https://github.com/Citeck/citeck-launcher/releases).
2. **Workspace-Archiv:** von [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (Abschnitt „Releases“ oder Schaltfläche „Download ZIP“). Dieses Archiv enthält die Bundle-
   Definitionen, die der Launcher normalerweise aus Git abrufen würde.

Anschließend auf dem Zielserver:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

Das Flag `--workspace` extrahiert die Bundle-Repos lokal, sodass beim Start kein Internet benötigt wird.
Um den Workspace später aus einem neuen Archiv zu aktualisieren, ohne neu zu installieren: `citeck update -f <zip>`.

## Desktop-App

Citeck Launcher ist auch als **Desktop-Anwendung** für Windows, macOS und Linux verfügbar —
derselbe Daemon und dieselbe Web-UI, eingebettet in ein natives Fenster (Wails). Die App überwacht den Daemon als
Kindprozess, sodass Ihre Container weiterlaufen, selbst wenn das Fenster geschlossen ist. Desktop-Installer sind jedem
[GitHub-Release](https://github.com/Citeck/citeck-launcher/releases) beigefügt; laden Sie denjenigen für Ihre
Plattform herunter:

| Betriebssystem | Datei | Architektur |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Jeder Installer hat eine `.sha256`-Begleitdatei zur Verifizierung. Ihre Daten bleiben bei Upgrades erhalten.

## Funktionen

- **Interaktiver Installer** mit TLS-Autoerkennung (Let's Encrypt / selbstsigniert / eigenes Zertifikat)
- **i18n** mit 8 Sprachen: Englisch, Russisch, Chinesisch, Spanisch, Deutsch, Französisch, Portugiesisch, Japanisch
- **Echtzeit-Updates** über SSE-Events (App-Status, Ressourcennutzung)
- **Volume-Snapshots** mit Export/Import (ZIP + tar.xz)
- **Let's Encrypt**-Integration mit automatischer Erneuerung (Domains und IP-Adressen)
- **Selbstheilende Laufzeitumgebung** mit Liveness-Probes, Neustart-Tracking und Diagnose vor dem Neustart
- **Shell-Vervollständigung** für bash, zsh, fish, PowerShell

## CLI-Verwendung

```
citeck install [--workspace <zip>]        Interactive setup wizard (offline with --workspace)
citeck start [app] [-d|--detach]          Start daemon/namespace (--detach = don't wait)
citeck stop [app...] [-d|--detach]        Stop namespace or app(s) (--detach = don't wait)
citeck restart [app] [-d|--detach]        Restart an app or the entire namespace (waits by default)
citeck reload [--dry-run] [-d|--detach]   Reload config and regenerate changed containers
citeck status [-w|--watch]                Show namespace status
citeck describe <app>                     Show container details (image, ports, env, volumes)
citeck health                             Health check (exit 0=healthy, 1=daemon down, 8=unhealthy)
citeck diagnose [--fix] [--dry-run]       Run diagnostics (with optional auto-fix)
citeck logs [app] [-f|--follow]           Stream logs (daemon if no app)
citeck exec <app> -- <command>            Execute command in container
citeck update [-f|--file <zip>]           Pull workspace/bundle defs (or import from ZIP)
citeck upgrade [bundle:version] [--yes]   Switch to a different bundle version
citeck snapshot list|export|import|delete Manage volume snapshots (auto stop/start)
citeck config view|validate|edit          Show, check, or edit namespace.yml
citeck setup [setting]                    Configure settings (TUI menu or by ID)
citeck setup history                      Show config change history
citeck clean [--force] [--volumes] [--images]  Clean orphaned resources / prune images
citeck dump-system-info [--full]          Collect diagnostics ZIP (status, logs, docker inspect, journalctl)
citeck version [--short]                  Show version info
citeck completion bash|zsh|fish           Generate shell completion
citeck uninstall [--delete-data]          Remove systemd service, binary, and (optionally) data
```

Globale Flags: `--format (text|json)`, `--yes/-y`.

## Konfiguration

Details zu `daemon.yml` und `namespace.yml` finden Sie in der [Konfigurationsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html).

## Lizenz

Siehe [LICENSE](../LICENSE).
