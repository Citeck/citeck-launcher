![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · **Deutsch** · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Die Citeck-Plattform betreiben — auf dem eigenen Rechner oder auf einem Server — aus einer einzigen Binärdatei.**

[Citeck](https://github.com/Citeck) ist eine selbstgehostete, quelloffene Alternative zu proprietären ECM/BPM-Suiten: Dokumente und Datensätze verwalten, Genehmigungs-Workflows mit einem integrierten BPMN-Designer automatisieren und interne Anwendungen — Portale, CRM, Fallbearbeitung — mit wenig oder ganz ohne Code erstellen. Benutzer, Rollen und Berechtigungen sind bereits eingebaut.

Von Hand betrieben bedeutet das, gut zwei Dutzend Docker-Dienste zu orchestrieren. Citeck Launcher nimmt Ihnen das ab: eine einzige, etwa 24 MB große Binärdatei, die die Plattform installiert, jeden Dienst (Keycloak, PostgreSQL, RabbitMQ und die Citeck-Webanwendungen) als Docker-Container ausführt, sie gesund hält und aktualisiert. Er ist der unterstützte Weg, Citeck zu betreiben — als Desktop-App oder von der Kommandozeile auf einem Server.

<!-- TODO(screenshot): add an English-locale screenshot of the launcher dashboard here, e.g.
     ![Citeck Launcher](docs/img/dashboard.png) -->

**Sie brauchen:** Docker · **16 GB** RAM für die Community-Edition, **24–32 GB** für Enterprise (~24 Dienste) · **50+ GB** freien Plattenplatz für Images und Daten. Unter Windows und macOS installieren Sie zuerst [Docker Desktop](https://www.docker.com/products/docker-desktop/).

## Desktop oder Server?

Zwei Betriebsarten — wählen Sie diejenige, die dazu passt, **wo** Citeck laufen soll:

| | 🖥 **Desktop-App** | 🖧 **Server (CLI)** |
|---|---|---|
| Geeignet für | Ihren eigenen Computer | Einen Linux-Server / eine VM (meist über SSH) |
| Installation | Installer herunterladen, durch den Assistenten klicken | Ein einziger `curl … \| bash`-Befehl |
| Oberfläche | Natives App-Fenster (GUI) | Terminal — `citeck`-CLI + Einrichtungsassistent |
| Hier starten | [Desktop-App](#desktop-app) | [Installation auf einem Server](#installation-auf-einem-server) |

> **Hinweis:** Der Schnellstart mit `curl … | bash` und die `citeck`-Befehle in dieser README sind für **Server-Installationen** gedacht. Auf Ihrem eigenen Computer betreiben Sie Citeck über die **Desktop-App** — dort wird alles über die Benutzeroberfläche erledigt.

## Desktop-App

Die Desktop-Anwendung führt Citeck auf Ihrem eigenen Windows-, macOS- oder Linux-Rechner aus — ein ganz normales App-Fenster, ohne Kommandozeile. Citeck läuft im Hintergrund weiter, auch nachdem Sie das Fenster geschlossen haben.

Installieren Sie zuerst Docker Desktop und laden Sie dann den Installer für Ihre Plattform aus dem [neuesten Release](https://github.com/Citeck/citeck-launcher/releases/latest) herunter:

| Betriebssystem | Datei | Architektur |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Jeder Installer hat eine `.sha256`-Begleitdatei zur Verifizierung. Ihre Daten bleiben bei Upgrades erhalten.

## Installation auf einem Server

> **Für einen Linux-Server oder eine VM** (amd64 oder arm64) — führen Sie diese Schritte auf dem Server aus, über SSH. Voraussetzung: Docker ist installiert und läuft.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Das Skript lädt das neueste Release für Ihre Plattform herunter, installiert es nach `/usr/local/bin/citeck` und startet anschließend den Einrichtungsassistenten (`citeck install`). Der Assistent ist **interaktiv und benötigt ein echtes Terminal**. Er fragt Sie nach:

- dem **Domainnamen oder der IP-Adresse**, über die Sie die Plattform im Browser erreichen wollen;
- der Art der **Verbindungsabsicherung** — automatisch, Let's Encrypt, ein selbstsigniertes Zertifikat, Ihr eigenes Zertifikat oder einfaches HTTP. (Let's Encrypt setzt einen öffentlichen DNS-Namen voraus, der auf diesen Host zeigt, sowie eingehenden Port 80; ist er nicht erreichbar, weicht der Assistent auf ein selbstsigniertes Zertifikat aus.)
- ob **Demo-Daten** eingespielt und ob ein **systemd-Service** installiert werden soll.

### Erster Start: was Sie erwartet

**Es dauert eine Weile — das ist normal.** Der Launcher lädt mehrere GB an Docker-Images herunter, danach braucht die Plattform selbst rund **10–15 Minuten**, um hochzufahren: Die Dienste starten in Abhängigkeitsreihenfolge, und Keycloak importiert beim ersten Start sein Realm. Beobachten Sie, wie die Apps eine nach der anderen auf `RUNNING` umspringen:

```bash
citeck status -w
```

Wenn alles läuft, gibt der Assistent Ihre Zugangsdaten aus:

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

Zwei Dinge, die Sie zu diesem Bildschirm wissen sollten:

- **Das Admin-Passwort wird nur einmal angezeigt.** Kopieren Sie es — später lässt es sich nicht wiederherstellen. Falls Sie es verlieren, setzen Sie es mit `citeck setup admin-password` zurück.
- **Bei einem selbstsignierten Zertifikat warnt Ihr Browser.** Das ist zu erwarten — klicken Sie auf *Erweitert* → *Fortfahren*.

Wenn nach etwa 20 Minuten immer noch etwas festzuhängen scheint, beginnen Sie mit `citeck diagnose` (mit `--fix` behebt der Befehl, was er beheben kann) und `citeck logs <app>`.

### Launcher aktualisieren

Führen Sie denselben Einzeiler erneut aus — das Skript erkennt die installierte Version, fordert zur Aktualisierung auf, stoppt den Daemon und ersetzt die Binärdatei. Die vorherige Binärdatei bleibt unter `/usr/local/bin/citeck.bak` erhalten und lässt sich mit `citeck install --rollback` wiederherstellen. Ihre Daten bleiben erhalten.

## Begriffe

Drei Begriffe, die Ihnen in der CLI und in der Dokumentation immer wieder begegnen:

- **Namespace** — eine isolierte Instanz der Plattform (mit eigenen Containern, Volumes und Daten). Hat nichts mit Linux- oder Kubernetes-Namespaces zu tun; es ist ein Konzept des Launchers. Ein typischer Server betreibt genau einen.
- **Bundle** — welche Apps in welchen Versionen ein Plattform-Release ausmachen, zum Beispiel ein Community- oder ein Enterprise-Release. `citeck upgrade <bundle:version>` wechselt zwischen ihnen.
- **Workspace** — woher diese Definitionen stammen (normalerweise ein Git-Repository oder eine `.zip`-Datei für Offline-Installationen ohne Netzzugang).

## Befehle für den Alltag (Servermodus)

Im Desktop-Modus sind dieselben Vorgänge über die Benutzeroberfläche der App verfügbar.

```bash
citeck status -w                 # Namespace und alle Apps beobachten
citeck logs <app> -f             # Logs streamen (ohne App = das Log des Daemons)
citeck stop <app>                # App stoppen — und über Neustarts hinweg gestoppt lassen
citeck start <app>               # sie wieder starten (erneut anbinden)
citeck reload                    # Konfigurationsänderungen anwenden, nur Geändertes neu erstellen
citeck snapshot export <name>    # alle Volumes sichern (stoppt die Plattform und startet sie danach neu)
citeck upgrade <bundle:version>  # auf eine andere Plattformversion wechseln
citeck diagnose --fix            # Health-Checks mit optionaler automatischer Reparatur
citeck setup                     # Einstellungen ändern (Admin-Passwort, TLS, E-Mail, Ressourcen …)
citeck edit <app>                # die Definition einer App bearbeiten, im Stil von kubectl edit
```

Beachten Sie: `citeck stop <app>` **löst die App ab** (detach): Sie bleibt über Neustarts und Reloads hinweg gestoppt, bis Sie `citeck start <app>` ausführen. Genau so geben Sie auf einem kleinen Host Arbeitsspeicher frei — ein paar optionale Apps abzulösen spart mehrere GB.

Globale Flags: `--format (text|json)` für Skripte, `--yes/-y` zum Überspringen von Rückfragen, `-d/--detach`, um sofort zurückzukehren statt zu warten. Vollständige Referenz: `citeck --help` oder die [Befehlsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).

## Was Sie bekommen

- **Selbstheilende Laufzeitumgebung** — Liveness-Probes starten abgestürzte Dienste neu, und der Launcher hält fest, warum sie abgestürzt sind
- **Sicherung und Wiederherstellung** — alle Volumes in ein einziges Archiv exportieren und auf diesem oder einem anderen Host wieder importieren
- **HTTPS ab Werk** — Let's Encrypt mit automatischer Erneuerung (Domains *und* IP-Adressen) oder Ihr eigenes Zertifikat
- **Status und Logs in Echtzeit** — Ressourcennutzung und Log-Streams für jeden Dienst, in der Desktop-App oder in der CLI
- Lokalisiert in 8 Sprachen, mit Shell-Vervollständigung für bash, zsh, fish und PowerShell

## Editionen

Die **Community**-Edition ist vollständig quelloffen und kostenlos und deckt die Kernfunktionalität der Plattform ab. Die kommerzielle **Enterprise**-Edition ergänzt sie um professionellen Support und zusätzliche Funktionen; ihre Installation erfordert einen von Citeck ausgestellten Lizenzschlüssel. Dieser Launcher installiert beide.

## Sicherheitsmodell

Das sagen wir lieber gleich vorweg: **Der Daemon im Servermodus steuert Docker, seine API ist daher als root-äquivalent auf dem Host zu behandeln** (`citeck exec` etwa führt Befehle innerhalb von Containern aus). Deshalb ist die sichere Variante die Voreinstellung.

- **Die CLI** kommuniziert mit dem Daemon über einen Unix-Socket, der auf den Benutzer des Daemons beschränkt ist (Modus 0600).
- **Die Web UI des Launchers ist im Servermodus standardmäßig deaktiviert** — die unterstützte Server-Schnittstelle ist die CLI/TUI. Wenn Sie sie aktivieren (`server.webui.enabled: true` in `daemon.yml`), lauscht der Daemon zusätzlich auf einem TCP-Port. Ein Localhost-Bind stellt die vollständige API nur mit Browser-CSRF-Schutz bereit — das ist **keine** Authentifizierung; jeder lokale Benutzer und jeder Prozess mit Zugriff auf den Port erhält damit volle Kontrolle. Aktivieren Sie sie also bewusst und nur auf einem **Single-Tenant-Host**, dessen lokalen Benutzern ohnehin allen Docker-/Root-Zugriff anvertraut ist. Binds außerhalb von Localhost erfordern mTLS-Client-Zertifikate.
- **Um diese Localhost-Lücke zu schließen**, aktivieren Sie die API-Token-Authentifizierung: `api_auth.enabled: true` in `daemon.yml`. Jede `/api`-Anfrage über TCP benötigt dann `Authorization: Bearer <token>` (oder das Browser-Sitzungscookie, das `GET /auth/session?token=…` ausstellt). Das Token stammt aus `api_auth.token` oder wird beim Start automatisch in `conf/api-token` (Modus 0600) erzeugt. `citeck ui` gibt einen authentifizierten Link aus — und öffnet ihn. Statische UI-Dateien bleiben öffentlich; geschützt wird nur die API. Der Unix-Socket, die Desktop-App und mTLS-Clients sind davon nicht betroffen.

(Dieser Abschnitt behandelt die Admin-Oberfläche des *Launchers*, nicht die Oberfläche der Citeck-Plattform, an der Sie sich nach der Installation anmelden.)

## Dokumentation

- **Servermodus:** [Installation und Konfiguration](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) (`daemon.yml` / `namespace.yml`) und die [Befehlsreferenz](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **Desktop-App:** [Doku zum Desktop-Modus](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)
- **Release Notes:** [CHANGELOG.md](../CHANGELOG.md)

## Entwicklung

Gebaut aus Go (Daemon + CLI) und React (eingebettete Web UI); die Desktop-App kapselt dieselbe Oberfläche in einer Wails-Webview. Voraussetzungen, Build-Targets und das vollständige lokale Prüf-Gate (`make check`) sind in [AGENTS.md](../AGENTS.md) dokumentiert.

## Lizenz und Kontakt

Citeck Launcher ist quelloffen unter der **LGPL-3.0**-Lizenz — siehe [LICENSE](../LICENSE).

Bei Fragen, zu Enterprise-Lizenzen oder für eine Beratung [nehmen Sie Kontakt mit dem Citeck-Team auf](https://www.citeck.ru/contacts/).
