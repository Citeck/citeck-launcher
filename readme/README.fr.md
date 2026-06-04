![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · **Français** · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**Toute votre plateforme Citeck, opérationnelle en une seule commande.**

Citeck Launcher est un installateur auto-hébergé et un gestionnaire de conteneurs pour la plateforme BPM/ECM low-code **Citeck**. Il s'agit d'un unique binaire d'environ 24 Mo qui fonctionne comme outil en ligne de commande, comme démon en arrière-plan et comme application de bureau multiplateforme, exécutant chaque service Citeck en tant que conteneur Docker et les regroupant dans des espaces de noms isolés.

[Citeck](https://github.com/Citeck) est une plateforme low-code open source pour la gestion de contenu d'entreprise (Enterprise Content Management, ECM) et la gestion des processus métier (Business Process Management, BPM). Le launcher installe, met à niveau et exploite une pile Citeck complète — Keycloak, PostgreSQL, RabbitMQ et les applications web Citeck — sur un seul hôte avec une seule commande.

> **Documentation complète :** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html

## Démarrage rapide

Prérequis : Docker (en cours d'exécution).

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Le script d'installation télécharge la dernière version pour votre plateforme et l'installe dans `/usr/local/bin/`. L'assistant configure l'espace de noms et démarre la plateforme.

> **Important :** La commande `citeck install` est un **assistant TUI interactif** et nécessite un véritable terminal. L'assistant affiche le mot de passe administrateur généré **une seule fois** à la fin — assurez-vous de le copier et de l'enregistrer, car vous ne pourrez pas le récupérer après avoir fermé l'écran. Si vous le perdez, réinitialisez-le via `citeck setup admin-password` (voir la [référence des commandes](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)). Appuyer sur `Ctrl+C` avant l'étape finale « écrire la configuration » quitte sans effectuer de modifications ; en cas d'interruption ultérieure, vérifiez `/opt/citeck/conf/` pour un état partiel.
>
> L'installation automatisée / non interactive est une fonctionnalité à venir — veuillez ouvrir un ticket si vous en avez besoin.

Pour **mettre à niveau** une installation existante, exécutez la même commande en une ligne — le script détecte la version installée, propose la mise à jour, arrête le démon et remplace le binaire (une sauvegarde est conservée dans `/usr/local/bin/citeck.bak`, restaurable via `citeck install --rollback`).

### Installation hors ligne

Pour les serveurs sans accès à Internet, téléchargez au préalable à la fois le binaire et l'archive de l'espace de travail :

1. **Binaire :** depuis la [page des versions](https://github.com/Citeck/citeck-launcher/releases).
2. **Archive de l'espace de travail :** depuis [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (section Releases, ou bouton « Download ZIP »). Cette archive contient les définitions de bundles
   que le launcher récupérerait normalement depuis git.

Puis, sur le serveur cible :

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

Le drapeau `--workspace` extrait les dépôts de bundles localement, de sorte qu'aucun accès à Internet n'est nécessaire au démarrage.
Pour mettre à jour ultérieurement l'espace de travail depuis une nouvelle archive sans réinstaller : `citeck update -f <zip>`.

## Application de bureau

Citeck Launcher est également disponible en tant qu'**application de bureau** pour Windows, macOS et Linux —
le même démon et la même interface Web encapsulés dans une fenêtre native (Wails). L'application supervise le démon en tant que
processus enfant, de sorte que vos conteneurs continuent de fonctionner même lorsque la fenêtre est fermée. Les installateurs de bureau sont joints à chaque
[version GitHub](https://github.com/Citeck/citeck-launcher/releases) ; téléchargez celui correspondant à votre
plateforme :

| OS | Fichier | Arch |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Chaque installateur dispose d'un fichier `.sha256` associé pour la vérification. Vos données sont préservées lors des mises à niveau.

## Fonctionnalités

- **Installateur interactif** avec détection automatique de TLS (Let's Encrypt / certificat auto-signé / certificat personnalisé)
- **i18n** avec 8 langues : anglais, russe, chinois, espagnol, allemand, français, portugais, japonais
- **Mises à jour en temps réel** via des événements SSE (état des applications, utilisation des ressources)
- **Instantanés de volumes** avec export/import (ZIP + tar.xz)
- **Intégration Let's Encrypt** avec renouvellement automatique (domaines et adresses IP)
- **Runtime auto-réparateur** avec sondes de vivacité, suivi des redémarrages et diagnostics avant redémarrage
- **Complétion shell** pour bash, zsh, fish, PowerShell

## Utilisation de la CLI

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

Drapeaux globaux : `--format (text|json)`, `--yes/-y`.

## Configuration

Consultez la [référence de configuration](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html) pour les détails sur `daemon.yml` et `namespace.yml`.

## Licence

Voir [LICENSE](../LICENSE).
