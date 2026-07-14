![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · **Français** · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**La façon officielle d'exécuter Citeck.**

🌐 [citeck.ru](https://www.citeck.ru) · ✉️ [Nous contacter](https://www.citeck.ru/contacts/) · 💬 [Communauté Telegram](https://telegram.me/citeck)

[Citeck](https://github.com/Citeck) est une plateforme low-code auto-hébergée et open source qui remplace les suites ECM/BPM propriétaires. Vous l'utilisez pour presque toutes les tâches impliquant des documents d'entreprise, de l'approbation des contrats et des achats aux processus RH, en passant par une archive électronique ou un portail d'entreprise. Vous dessinez le parcours de chaque processus dans le concepteur BPMN intégré et configurez les types de documents sans code ; utilisateurs, rôles et permissions sont fournis d'emblée.

Citeck Launcher est le moyen le plus simple de mettre la plateforme en route et de l'y maintenir. Vous téléchargez un unique binaire d'environ 24 Mo : il installe la plateforme et démarre ses services via Docker. Ensuite, Launcher surveille leur état de santé et redémarre tout ce qui tombe, et il rend la mise à niveau de la plateforme simple et prévisible. Sur votre propre ordinateur, il fonctionne comme application de bureau ; sur un serveur, en ligne de commande.

![Citeck Launcher dashboard](screenshots/running.png)

**Il vous faudra :** Docker · **16 Go** de RAM pour l'édition Community, **24 à 32 Go** pour Enterprise (~24 services) · **plus de 50 Go** d'espace disque libre pour les images et les données. Sous Windows et macOS, installez d'abord [Docker Desktop](https://www.docker.com/products/docker-desktop/).

## Bureau ou serveur ?

Deux façons de l'exécuter — choisissez celle qui correspond à **l'endroit** où vous voulez faire tourner Citeck :

| | 🖥 **Application de bureau** | 🖧 **Serveur (CLI)** |
|---|---|---|
| Pour | Votre propre ordinateur | Un serveur / une VM Linux (généralement via SSH) |
| Installation | Téléchargez un installateur, suivez l'assistant | Une seule commande `curl … \| bash` |
| Interface | Fenêtre native de l'application (GUI) | Terminal — CLI `citeck` + assistant de configuration |
| Commencez ici | [Application de bureau](#application-de-bureau) | [Installation sur un serveur](#installation-sur-un-serveur) |

> **À noter :** le démarrage rapide `curl … | bash` et les commandes `citeck` de ce README concernent les **installations sur serveur**. Sur votre propre ordinateur, exécutez Citeck via l'**application de bureau** — tout s'y fait depuis l'interface.

## Application de bureau

L'application de bureau exécute Citeck sur votre propre machine Windows, macOS ou Linux — une fenêtre d'application classique, sans ligne de commande. Citeck continue de fonctionner en arrière-plan même après la fermeture de la fenêtre.

Installez d'abord Docker Desktop, puis téléchargez l'installateur correspondant à votre plateforme depuis la [dernière version](https://github.com/Citeck/citeck-launcher/releases/latest) :

| OS | Fichier | Arch |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Chaque installateur est accompagné d'un fichier `.sha256` pour la vérification. Vos données sont préservées lors des mises à niveau.

## Installation sur un serveur

> **Pour un serveur ou une VM Linux** (amd64 ou arm64) — exécutez ces étapes sur le serveur, via SSH. Prérequis : Docker est installé et en cours d'exécution.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Le script télécharge la dernière version pour votre plateforme, l'installe dans `/usr/local/bin/citeck`, puis lance l'assistant de configuration (`citeck install`). L'assistant est **interactif et nécessite un véritable terminal**. Il vous demande :

- le **nom de domaine ou l'adresse IP** que vous utiliserez pour accéder à la plateforme depuis un navigateur ;
- comment **sécuriser la connexion** — automatique, Let's Encrypt, un certificat auto-signé, votre propre certificat, ou HTTP simple. (Let's Encrypt nécessite un nom DNS public pointant vers cet hôte et le port 80 ouvert en entrée ; si l'hôte n'est pas joignable, l'assistant bascule sur un certificat auto-signé.)
- s'il faut déployer des **données de démonstration**, et s'il faut installer un **service systemd**.

### Premier démarrage : à quoi s'attendre

**Cela prend du temps — c'est normal.** Le launcher télécharge plusieurs Go d'images Docker, puis la plateforme elle-même met environ **10 à 15 minutes** à démarrer : les services démarrent dans l'ordre de leurs dépendances, et Keycloak importe son realm au premier démarrage. Regardez les applications passer une à une à l'état `RUNNING` :

```bash
citeck status -w
```

Une fois tout démarré, l'assistant affiche vos informations d'accès :

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

Deux points importants à propos de cet écran :

- **Le mot de passe administrateur n'est affiché qu'une seule fois.** Copiez-le — il est impossible de le récupérer ensuite. Si vous le perdez, réinitialisez-le avec `citeck setup admin-password`.
- **Avec un certificat auto-signé, votre navigateur affichera un avertissement.** C'est attendu — cliquez sur *Avancé* → *Continuer*.

Si quelque chose semble toujours bloqué après une vingtaine de minutes, commencez par `citeck diagnose` (ajoutez `--fix` pour le laisser réparer ce qu'il peut) et `citeck logs <app>`.

### Mettre à niveau le launcher

Réexécutez la même commande en une ligne — le script détecte la version installée, propose la mise à jour, arrête le démon et remplace le binaire. Le binaire précédent est conservé dans `/usr/local/bin/citeck.bak` et restaurable via `citeck install --rollback`. Vos données sont préservées.

## Concepts

Trois mots que l'on retrouve partout dans la CLI et la documentation :

- **Namespace** — une instance isolée de la plateforme (ses propres conteneurs, volumes et données). Rien à voir avec les namespaces Linux ou Kubernetes ; c'est une notion propre au launcher. Un serveur typique n'en exécute qu'un seul.
- **Bundle** — quelles applications et quelles versions composent une release de la plateforme, par exemple une release Community ou Enterprise. `citeck upgrade <bundle:version>` permet de passer de l'une à l'autre.
- **Workspace** — l'origine de ces définitions (normalement un dépôt Git, ou un `.zip` hors ligne pour les installations en environnement isolé).

## Commandes du quotidien (mode serveur)

En mode bureau, les mêmes opérations sont accessibles depuis l'interface de l'application.

```bash
citeck status -w                 # suivre l'état du namespace et de chaque application
citeck logs <app> -f             # diffuser les logs (sans application = le log du démon)
citeck stop <app>                # arrêter une application — et la garder arrêtée après un redémarrage
citeck start <app>               # la redémarrer (rattachement)
citeck reload                    # appliquer les changements de config, recréer uniquement ce qui a changé
citeck snapshot export <name>    # sauvegarder tous les volumes (arrête la plateforme, puis la relance)
citeck upgrade <bundle:version>  # basculer vers une autre version de la plateforme
citeck diagnose --fix            # contrôles de santé avec réparation automatique optionnelle
citeck setup                     # modifier les paramètres (mot de passe admin, TLS, e-mail, ressources…)
citeck edit <app>                # éditer la définition d'une application, à la manière de kubectl edit
```

Notez que `citeck stop <app>` **détache** l'application : elle reste arrêtée malgré les redémarrages et les rechargements jusqu'à ce que vous exécutiez `citeck start <app>`. C'est aussi le moyen de libérer de la mémoire sur un hôte modeste — détacher quelques applications optionnelles économise plusieurs Go.

Options globales : `--format (text|json)` pour le scripting, `--yes/-y` pour ignorer les confirmations, `-d/--detach` pour rendre la main immédiatement au lieu d'attendre. Référence complète : `citeck --help` ou la [référence des commandes](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).

## Ce que vous obtenez

- **Runtime auto-réparateur** — les sondes de vivacité redémarrent les services plantés, et le launcher consigne la raison du plantage
- **Sauvegarde et restauration** — exportez tous les volumes dans une seule archive, réimportez-les sur cet hôte ou sur un autre
- **HTTPS prêt à l'emploi** — Let's Encrypt avec renouvellement automatique (domaines *et* adresses IP), ou votre propre certificat
- **État et logs en direct** — utilisation des ressources et diffusion des logs pour chaque service, dans l'application de bureau ou la CLI
- Localisé en 8 langues, avec complétion shell pour bash, zsh, fish et PowerShell

## Éditions

L'édition **Community** est entièrement open source et gratuite, et couvre les fonctionnalités de base de la plateforme. L'édition commerciale **Enterprise** ajoute un support professionnel et des fonctionnalités supplémentaires ; son installation nécessite une clé de licence délivrée par Citeck. Ce launcher installe l'une ou l'autre.

## Documentation

- **Mode serveur :** [installation et configuration](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) (`daemon.yml` / `namespace.yml`) et la [référence des commandes](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **Application de bureau :** [documentation du mode bureau](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)

## Licence

Citeck Launcher est un logiciel open source sous licence **LGPL-3.0** — voir [LICENSE](../LICENSE).
