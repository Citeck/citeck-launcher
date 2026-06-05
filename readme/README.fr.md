![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · **Français** · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Installez et exécutez une plateforme Citeck complète — en tant qu'application de bureau sur votre ordinateur, ou avec une seule commande sur un serveur.**

Citeck Launcher est l'installateur officiel et le gestionnaire de conteneurs de la plateforme BPM/ECM low-code **Citeck**. Un unique binaire d'environ 24 Mo fonctionne comme outil en ligne de commande, comme démon en arrière-plan et comme application de bureau multiplateforme — exécutant chaque service Citeck (Keycloak, PostgreSQL, RabbitMQ et les applications web Citeck) en tant que conteneur Docker et les regroupant dans des espaces de noms isolés. Les applications et versions à exécuter sont définies par un **bundle** (par exemple, une édition Community ou Enterprise).

[Citeck](https://github.com/Citeck) est une plateforme open source de création d'applications métier, combinant les approches **no-code, low-code et pro-code** pour gérer le contenu et les processus. Concrètement, vous l'utilisez pour **gérer des documents et des enregistrements (ECM), automatiser des processus métier et des flux d'approbation grâce à un concepteur BPMN intégré, et créer des applications internes — portails, CRM, gestion de dossiers — avec peu ou pas de code**, avec comptes utilisateurs, rôles et permissions intégrés. C'est une alternative auto-hébergée aux suites ECM/BPM propriétaires, adaptée à tous, des analystes métier aux développeurs.

L'édition **Community** est entièrement open source et gratuite — elle couvre les fonctionnalités de base de la plateforme et est conçue pour être conviviale aux extensions de tout type. Pour des configurations plus exigeantes, l'édition **Enterprise** commerciale ajoute un support professionnel et des fonctionnalités d'entreprise supplémentaires. Ce launcher installe l'une ou l'autre édition. Pour toute question ou une consultation, [contactez l'équipe Citeck](https://www.citeck.ru/contacts/).

## Bureau ou serveur ?

Il existe deux façons de l'exécuter — choisissez celle qui correspond à **l'endroit** où vous souhaitez faire tourner Citeck :

| | 🖥 **Application de bureau** | 🖧 **Serveur (CLI)** |
|---|---|---|
| Pour | Votre propre ordinateur | Un serveur / une VM Linux (généralement via SSH) |
| Installation | Téléchargez un installateur, suivez l'assistant | Une seule commande `curl … \| bash` |
| Interface | Fenêtre native de l'application (GUI) | Terminal — CLI `citeck` + assistant de configuration (TUI) |
| Commencez ici | [Application de bureau](#application-de-bureau) | [Installation sur un serveur](#installation-sur-un-serveur) |

> **À noter :** le démarrage rapide `curl … | bash` et la CLI `citeck` présentés dans ce README concernent les **installations sur serveur**. Sur votre propre ordinateur, exécutez Citeck via l'**application de bureau** — tout s'y fait depuis l'interface.

**Prérequis :** Docker ; environ **16 Go** de RAM pour l’édition Community (**24 Go** pour les ~24 services d’Enterprise) ; et des dizaines de Go de disque pour les images.

## Application de bureau

L'**application de bureau** exécute Citeck sur votre propre machine Windows, macOS ou Linux : une fenêtre d'application classique, sans ligne de commande. Citeck continue de fonctionner en arrière-plan même après la fermeture de la fenêtre.

Les installateurs de bureau sont joints à chaque [version GitHub](https://github.com/Citeck/citeck-launcher/releases) — téléchargez celui correspondant à votre plateforme :

| OS | Fichier | Arch |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Chaque installateur dispose d'un fichier `.sha256` associé pour la vérification. Vos données sont préservées lors des mises à niveau.

## Installation sur un serveur

> **Pour un serveur ou une VM Linux** — exécutez ces étapes sur le serveur, via SSH.

Prérequis : un hôte Linux avec Docker en cours d'exécution.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Le script d'installation télécharge la dernière version pour votre plateforme et l'installe dans `/usr/local/bin/`. L'assistant configure ensuite l'espace de noms et démarre la plateforme.

> **Important :** La commande `citeck install` est un **assistant TUI interactif** et nécessite un véritable terminal. L'assistant affiche le mot de passe administrateur généré **une seule fois** à la fin — copiez-le et enregistrez-le, car vous ne pourrez pas le récupérer après avoir fermé l'écran. Si vous le perdez, réinitialisez-le via `citeck setup admin-password` (voir la [référence des commandes](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)).

Pour **mettre à niveau** une installation serveur existante, exécutez la même commande en une ligne — le script détecte la version installée, propose la mise à jour, arrête le démon et remplace le binaire (une sauvegarde est conservée dans `/usr/local/bin/citeck.bak`, restaurable via `citeck install --rollback`).

## Fonctionnalités

- **Installateur interactif** avec détection automatique de TLS (Let's Encrypt / certificat auto-signé / certificat personnalisé)
- **i18n** avec 8 langues : anglais, russe, chinois, espagnol, allemand, français, portugais, japonais
- **Mises à jour en temps réel** via des événements SSE (état des applications, utilisation des ressources)
- **Instantanés de volumes** avec export/import (ZIP + tar.xz)
- **Intégration Let's Encrypt** avec renouvellement automatique (domaines et adresses IP)
- **Runtime auto-réparateur** avec sondes de vivacité, suivi des redémarrages et diagnostics avant redémarrage
- **Complétion shell** pour bash, zsh, fish, PowerShell

## Utilisation de la CLI (mode serveur)

Ces commandes gèrent une installation en **mode serveur** depuis la CLI. (En mode bureau, les mêmes opérations sont disponibles depuis l'interface de l'application.)

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

## Documentation

- **Mode serveur :** [Documentation du mode serveur du launcher](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) — installation, configuration (`daemon.yml` / `namespace.yml`) et la [référence des commandes](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).
- **Application de bureau :** [Documentation du mode bureau](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html).

## Licence

Citeck Launcher est un logiciel open source sous licence **LGPL-3.0** — voir [LICENSE](../LICENSE).
