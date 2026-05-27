# Citeck Launcher 1.x → 2.x Porting Specification

Полное описание текущего desktop-приложения (Kotlin/Compose Desktop, ветка `release/1.4.1`) — для портирования на новую веб-версию 2.x (Go-демон + React SPA).

## Аудитория

Разработчик, который будет писать или ревьюить Go-демон или React UI новой версии. Спецификация описывает **что должно делать приложение**, со ссылками на исходники Kotlin-версии для верификации.

## Источники

Документ собран из 6 параллельных исследований кодовой базы и кросс-сверен с состоянием 2.x. Все факты ссылаются на конкретные файлы и строки в `src/main/kotlin/` или `src/main/resources/`.

**Update (commit `6fe02d1`, 2026-05-27):** все numbered items 1–26 + multi-workspace polish 11a–11d + Doubtful A/B/C/F закрыты. Открытыми остаются только два platform-specific verification теста (macOS Retina tray, GTK tray fallback) — см. `REMAINING.md`. Migration с Kotlin 1.x теперь pure-Go (см. `07` §5).

## Содержание

| # | Файл | О чём |
|---|---|---|
| 01 | [architecture-and-lifecycle.md](01-architecture-and-lifecycle.md) | Архитектура, AppDir, точка входа, single-instance lock, IPC, трей, тема, packaging |
| 02 | [ui-shell-and-screens.md](02-ui-shell-and-screens.md) | Общая UI-модель (MutProp, Popup/Dialog/Window, ContextMenu) и все экраны (Welcome / Loading / DockerNotAvailable / Namespace) |
| 03 | [dialogs-forms-editor.md](03-dialogs-forms-editor.md) | Общие диалоги, AppCfgEditWindow, SnapshotsDialog, форм-фреймворк, EditorWindow |
| 04 | [tables-logs-actions.md](04-tables-logs-actions.md) | DataTable DSL, LogsViewer, CiteckSelect, Action-система, каталог иконок |
| 05 | [workspaces-bundles-cloud.md](05-workspaces-bundles-cloud.md) | Workspaces, BundleKey/Ref, BundlesService, CloudConfigServer (Spring Cloud Config на 8761) |
| 06 | [entities-secrets-license.md](06-entities-secrets-license.md) | Generic entity framework, AuthSecrets, LicenseService |
| 07 | [git-database-snapshots.md](07-git-database-snapshots.md) | JGit-интеграция, H2 MVStore + Repository, WorkspaceSnapshots (резюмируемая загрузка) |
| 08 | [namespace-runtime-and-generator.md](08-namespace-runtime-and-generator.md) | NamespaceConfig, ApplicationDef, NamespaceGenerator (12-шаговая генерация), AppRuntimeStatus state machine, NsRuntimeFiles |
| 09 | [docker-and-appfiles.md](09-docker-and-appfiles.md) | DockerApi, DockerLabels (контракт обнаружения), DockerConstants (naming), Pull/Start/Stop actions, дефолтные appfiles (alfresco / keycloak / pgadmin / postgres / proxy) |
| 10 | [2x-status-and-porting-checklist.md](10-2x-status-and-porting-checklist.md) | Что уже сделано в 2.x (internal/ + web/), coverage-матрица, gap analysis, чек-лист критичных вещей которые нельзя сломать |
| — | [REMAINING.md](REMAINING.md) | Хронологический log items 1–26 + 11a–11d + Doubtful A–F; status markers; what's left (только macOS Retina + GTK tray) |
| — | [ROLLBACK.md](ROLLBACK.md) | Go 2.x desktop → Kotlin 1.x rollback procedure (storage.db read-only contract, что preserved, container hash drift) |

## Read order для портёров

1. **Если ты пишешь Go-демон**: 01 → 09 → 08 → 07 → 06 → 05 → 10.
2. **Если ты пишешь React-UI**: 01 (раздел про MutProp) → 02 → 03 → 04 → 10.
3. **Если делаешь end-to-end review**: 10 первый (что уже есть), потом по порядку.

## Ключевые цифры и константы

| Параметр | Значение | Где |
|---|---|---|
| Запуск приложения, мин. RAM | `-Xmx200m` (JVM heap) | `build.gradle.kts:126` |
| AppDir (Linux) | `$HOME/.citeck/launcher/` | `core/config/AppDir.kt` |
| Размер окна по умолчанию | 1200×800 dp | `Main.kt:101` |
| Cloud Config Server | HTTP, порт **8761** | `core/config/cloud/CloudConfigServer.kt` |
| Динамические порты webapps | счётчик от **17020** | `core/namespace/gen/NsGenContext.kt` |
| Дефолтный workspace ID | `"default"`, repo `https://github.com/Citeck/launcher-workspace.git` | `core/workspace/WorkspaceDto.kt` |
| Лимит логов в LogsState | задаётся caller'ом (обычно 5000) | `core/namespace/...` |
| Конкуррентные image pulls | `Semaphore(4)` | `runtime/actions/AppImagePullAction.kt` |
| Container hash label | `citeck.launcher.app.hash` | `core/namespace/runtime/docker/DockerLabels.kt` |
| Volume scoping pattern | `citeck_volume_{name}_{ns}_{ws}` | `core/namespace/runtime/docker/DockerConstants.kt` |
| Docker-compose project label | `citeck_launcher_{ns}_{ws}` | `DockerConstants.getDockerProjectName` |

## Что НЕ покрывается этим документом

- Тесты — описаны только структурно, без перечисления каждого test case.
- Build-pipeline (Gradle tasks помимо стандартных) — кратко.
- Стиль кода, ktlint — см. `CLAUDE.md` корня репо.
- Уже отрефлексированные в `.audit-backlog.md` нюансы 2.x (Phase 22 etc.) — упомянуты только там, где влияют на дизайн.
