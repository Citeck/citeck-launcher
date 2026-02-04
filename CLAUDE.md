# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher is a Kotlin desktop application for managing Citeck ECOS namespaces and Docker containers. It uses Jetbrains Compose Desktop for UI, targets Java 25, and builds with Gradle (Kotlin DSL).

## Build & Development Commands

```bash
./gradlew :app:run                               # Run the application
./gradlew test                                   # Run all tests
./gradlew test --tests "*.BundleKeyTest"         # Run a single test class
./gradlew ktlintCheck                            # Lint check
./gradlew ktlintFormat                           # Auto-fix lint issues
./gradlew build                                  # Full build
./gradlew :core:build                            # Build core module only (no Compose)
./gradlew :app:build                             # Build app module (with Compose)
./gradlew :app:shadowJar                         # Build fat JAR (output: app/build/compose/jars/)
./gradlew :app:packageDist -PtargetOs=linux_x64  # Build native distribution
```

Build output paths for `packageDist`:
- Linux: `app/build/compose/binaries/main/deb/`
- macOS: `app/build/compose/binaries/main/dmg/`
- Windows: `app/build/compose/binaries/main/msi/`

A ktlint pre-commit hook is installed automatically on first build (`addKtlintFormatGitPreCommitHook` task).

The `generateBuildInfo` task produces `build-info.json` (version, buildTime, javaVersion) as a generated resource.

## Architecture

### Service Layer (`core/`)

- **LauncherServices** — top-level IoC container that lazily initializes all application-wide services (database, Docker API, Git, secrets, cloud config, workspace selection)
- **WorkspaceServices** — workspace-scoped service container (namespaces, bundles, entities, licenses, snapshots, state persistence)
- **MutProp<T>** — reactive property wrapper used throughout for state management, similar to mutable state (lives in `core/utils/prop/`)

### Key Subsystems

| Package | Purpose |
|---|---|
| `core/namespace/` | Docker namespace lifecycle (start, stop, configure containers) |
| `core/bundle/` | Bundle definitions and versioning for application packages |
| `core/config/` | Configuration management and persistence |
| `core/database/` | H2 embedded database with `DataRepo` key-value abstraction |
| `core/entity/` | Generic entity/DTO definition and CRUD framework |
| `core/git/` | JGit integration for repository operations |
| `core/secrets/` | Secrets storage and authentication (Basic + Keycloak) |
| `core/actions/` | Pluggable action execution system |
| `core/socket/` | IPC via local sockets (single-instance lock) |
| `core/appdef/` | Application definition models (ApplicationDef, probes, resources, init containers) |
| `core/license/` | Enterprise license validation and storage |
| `core/logs/` | Custom Logback configuration |
| `core/snapshot/` | Workspace/volume snapshot download and restore |
| `core/workspace/` | Multi-workspace lifecycle management |
| `core/utils/` | Utilities: reactive props, JSON/YAML, file ops, promises, hashing |

### UI Layer (`view/`)

Built with Jetbrains Compose Desktop and Material 3. Key areas:

- `view/screen/` — main screens (Welcome, Namespace, Loading)
- `view/dialog/` — dialog components
- `view/form/` — form framework with components (Select, Journal)
- `view/table/` — table components with DSL builder
- `view/logs/` — log viewer with filtering and search
- `view/tray/` — system tray integration (includes GTK support on Linux)
- `view/theme/` — `LauncherTheme` theming system
- `view/editor/` — Syntax-highlighted text editor (RSyntaxTextArea)
- `view/popup/` — Window/dialog/popup abstractions (`CiteckWindow`, `CiteckPopup`)
- `view/commons/` — Shared components (tooltips, context menus, error dialogs)
- `view/action/` — Reusable action button components

### Entry Point

`Main.kt` (`ru.citeck.launcher.MainKt`) — handles application lock, service initialization, window management, and tray integration.

### Resources (`core/src/main/resources/appfiles/`)

Contains default configuration templates for managed services: Alfresco, PostgreSQL, PgAdmin, Proxy, Keycloak.

## Code Style

- Kotlin with IntelliJ IDEA code style (enforced by ktlint via `.editorconfig`)
- Wildcard imports are allowed (`ktlint_standard_no-wildcard-imports = disabled`)
- Trailing commas are disabled
- Composable function naming rules are relaxed (`@Composable` annotated functions ignore naming convention)
- GTK-related code (`src/**gtk/**`) has relaxed function naming rules
- 4-space indentation, LF line endings, UTF-8
- Additional disabled ktlint rules: `no-empty-first-line-in-method-block`, `no-empty-first-line-in-class-body`, `function-expression-body`, `multiline-expression-wrapping`, `spacing-between-declarations-with-annotations`

## Key Dependencies

- **UI**: Jetbrains Compose Desktop, Material 3
- **Docker**: docker-java (core + httpclient5 transport)
- **Git**: JGit
- **Database**: H2
- **Networking**: Ktor (client + server)
- **Serialization**: Jackson (JSON), SnakeYAML Engine
- **Logging**: Logback + kotlin-logging
- **Editor**: RSyntaxTextArea
- **SVG**: Apache Batik
- **Hashing**: Hash4J
- **Testing**: kotlin-test, AssertJ

## CI/CD

GitHub Actions release workflow (`.github/workflows/release.yml`): triggered by `v*.*.*` tags, builds on Linux/Windows/macOS (x64 + arm64), creates GitHub release.
