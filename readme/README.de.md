![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · **Deutsch** · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Installieren und betreiben Sie eine vollständige Citeck-Plattform — als Desktop-App auf Ihrem Computer oder mit einem einzigen Befehl auf einem Server.**

Citeck Launcher ist der offizielle Installer und Container-Manager für die Low-Code-BPM/ECM-Plattform **Citeck**. Eine einzige, etwa 24 MB große Binärdatei funktioniert als Kommandozeilen-Tool, als Hintergrund-Daemon und als plattformübergreifende Desktop-App — sie führt jeden Citeck-Dienst (Keycloak, PostgreSQL, RabbitMQ und die Citeck-Webanwendungen) als Docker-Container aus und gruppiert sie in isolierten Namespaces. Welche Apps und Versionen ausgeführt werden, legt ein **bundle** fest (zum Beispiel ein Community- oder Enterprise-Release).

[Citeck](https://github.com/Citeck) ist eine quelloffene Plattform zum Erstellen von Geschäftsanwendungen, die **No-Code-, Low-Code- und Pro-Code**-Ansätze kombiniert, um Inhalte und Prozesse zu verwalten. In der Praxis nutzen Sie sie, um **Dokumente und Datensätze zu verwalten (ECM), Geschäftsprozesse und Genehmigungs-Workflows mit einem integrierten BPMN-Designer zu automatisieren und interne Anwendungen — Portale, CRM, Fallbearbeitung — mit wenig oder gar keinem Code zu erstellen**, mit integrierten Benutzerkonten, Rollen und Berechtigungen. Es ist eine selbstgehostete Alternative zu proprietären ECM/BPM-Suiten, geeignet für alle — von Business-Analysten bis zu Entwicklern.

Die **Community**-Edition ist vollständig quelloffen und kostenlos — sie deckt die Kernfunktionalität der Plattform ab und ist darauf ausgelegt, Erweiterungen jeder Art entgegenzukommen. Für anspruchsvollere Setups bietet die kommerzielle **Enterprise**-Edition professionellen Support und zusätzliche Enterprise-Funktionen. Dieser Launcher installiert beide Editionen. Bei Fragen oder für eine Beratung [nehmen Sie Kontakt mit dem Citeck-Team auf](https://www.citeck.ru/contacts/).

## Desktop oder Server?

Es gibt zwei Möglichkeiten, den Launcher zu betreiben — wählen Sie diejenige, die dazu passt, **wo** Citeck laufen soll:

| | 🖥 **Desktop-App** | 🖧 **Server (CLI)** |
|---|---|---|
| Geeignet für | Ihren eigenen Computer | Einen Linux-Server / eine VM (meist über SSH) |
| Installation | Installer herunterladen, durch den Assistenten klicken | Ein einziger `curl … \| bash`-Befehl |
| Oberfläche | Natives App-Fenster (GUI) | Terminal — `citeck`-CLI + Einrichtungsassistent (TUI) |
| Hier starten | [Desktop-App](#desktop-app) | [Installation auf einem Server](#installation-auf-einem-server) |

> **Hinweis:** Der Schnellstart mit `curl … | bash` und die `citeck`-CLI in dieser README sind für **Server-Installationen** gedacht. Auf Ihrem eigenen Computer betreiben Sie Citeck über die **Desktop-App** — dort wird alles über die Benutzeroberfläche erledigt.

**Anforderungen:** Docker und genügend RAM für den Stack — etwa **16 GB** für die Community-Edition und **24–32 GB** für die ~24 Dienste von Enterprise, plus einige zehn GB Plattenplatz für die Images. Für eine vollständige ECM/BPM-Plattform dieser Art ist das wenig.

## Desktop-App

Die **Desktop-Anwendung** führt Citeck auf Ihrem eigenen Windows-, macOS- oder Linux-Rechner aus — ein ganz normales App-Fenster, ohne Kommandozeile. Citeck läuft im Hintergrund weiter, auch nachdem Sie das Fenster geschlossen haben.

Desktop-Installer sind jedem [GitHub-Release](https://github.com/Citeck/citeck-launcher/releases) beigefügt — laden Sie denjenigen für Ihre Plattform herunter:

| Betriebssystem | Datei | Architektur |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Jeder Installer hat eine `.sha256`-Begleitdatei zur Verifizierung. Ihre Daten bleiben bei Upgrades erhalten.

## Installation auf einem Server

> **Für einen Linux-Server oder eine VM** — führen Sie diese Schritte auf dem Server aus, über SSH.

Voraussetzungen: ein Linux-Host mit laufendem Docker.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Das Installationsskript lädt die neueste Version für Ihre Plattform herunter und installiert sie nach `/usr/local/bin/`. Der Assistent richtet anschließend den Namespace ein und startet die Plattform.

> **Wichtig:** Der Befehl `citeck install` ist ein **interaktiver TUI-Assistent** und erfordert ein echtes Terminal. Der Assistent gibt das generierte Admin-Passwort am Ende **einmalig** aus — kopieren und speichern Sie es unbedingt, da Sie es nach dem Schließen des Bildschirms nicht wiederherstellen können. Falls Sie es verlieren, setzen Sie es über `citeck setup admin-password` zurück (siehe die [Befehlsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)).

Um eine bestehende Server-Installation zu **aktualisieren**, führen Sie denselben Einzeiler aus — das Skript erkennt die installierte Version, fordert zur Aktualisierung auf, stoppt den Daemon und ersetzt die Binärdatei (ein Backup wird unter `/usr/local/bin/citeck.bak` aufbewahrt und kann über `citeck install --rollback` wiederhergestellt werden).

### Offline-Installation (Server)

Für Server ohne Internetzugang laden Sie sowohl die Binärdatei als auch das Workspace-Archiv vorab herunter:

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

## Funktionen

- **Interaktiver Installer** mit TLS-Autoerkennung (Let's Encrypt / selbstsigniert / eigenes Zertifikat)
- **i18n** mit 8 Sprachen: Englisch, Russisch, Chinesisch, Spanisch, Deutsch, Französisch, Portugiesisch, Japanisch
- **Echtzeit-Updates** über SSE-Events (App-Status, Ressourcennutzung)
- **Volume-Snapshots** mit Export/Import (ZIP + tar.xz)
- **Let's Encrypt**-Integration mit automatischer Erneuerung (Domains und IP-Adressen)
- **Selbstheilende Laufzeitumgebung** mit Liveness-Probes, Neustart-Tracking und Diagnose vor dem Neustart
- **Shell-Vervollständigung** für bash, zsh, fish, PowerShell

## CLI-Verwendung (Servermodus)

Diese Befehle verwalten eine Installation im **Servermodus** über die CLI. (Im Desktop-Modus sind dieselben Vorgänge über die Benutzeroberfläche der App verfügbar.)

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

## Dokumentation

- **Servermodus:** [Dokumentation zum Launcher-Servermodus](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) — Installation, Konfiguration (`daemon.yml` / `namespace.yml`) und die [Befehlsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).
- **Desktop-App:** [Doku zum Desktop-Modus](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html).

## Lizenz

Citeck Launcher ist quelloffen unter der **LGPL-3.0**-Lizenz — siehe [LICENSE](../LICENSE).
