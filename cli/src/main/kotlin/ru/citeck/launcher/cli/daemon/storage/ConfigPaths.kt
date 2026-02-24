package ru.citeck.launcher.cli.daemon.storage

import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.secrets.auth.AuthType
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.nio.file.Path
import java.nio.file.Paths
import java.time.Duration
import kotlin.io.path.exists

object ConfigPaths {

    const val CONFIG_VERSION_MAX = 1

    val HOME_DIR: Path = Paths.get(
        System.getProperty("citeck.home") ?: "/opt/citeck"
    )

    // Config files (admin-editable)
    val NAMESPACE_CONFIG: Path = HOME_DIR.resolve("conf/namespace.yml")

    // TLS certificates
    val TLS_DIR: Path = HOME_DIR.resolve("conf/tls")

    // Persistent data
    val DATA_DIR: Path = HOME_DIR.resolve("data")
    val ACME_DIR: Path = DATA_DIR.resolve("acme")
    val BUNDLES_DIR: Path = DATA_DIR.resolve("bundles")
    val VOLUMES_DIR: Path = DATA_DIR.resolve("volumes")
    val WORKSPACE_REPO_DIR: Path = DATA_DIR.resolve("workspace")
    val SNAPSHOTS_DIR: Path = DATA_DIR.resolve("snapshots")
    val RUNTIME_FILES_DIR: Path = DATA_DIR.resolve("rtfiles")
    val RUNTIME_STATE_FILE: Path = DATA_DIR.resolve("runtime.yml")

    // Logs
    val LOG_DIR: Path = HOME_DIR.resolve("log")

    const val WORKSPACE_REPO_URL = "https://github.com/Citeck/launcher-workspace.git"
    const val WORKSPACE_REPO_BRANCH = "main"

    fun findWorkspaceConfigInDir(dir: Path): Path? {
        if (!dir.exists()) return null
        var cfgVersion = CONFIG_VERSION_MAX + 1
        do {
            cfgVersion--
            val configName = if (cfgVersion == 0) "workspace.yml" else "workspace-v$cfgVersion.yml"
            val configFile = dir.resolve(configName)
            if (configFile.exists()) return configFile
        } while (cfgVersion > 0)
        return null
    }

    /**
     * Load workspace config:
     * 1. Workspace repo dir (offline archive or existing clone)
     * 2. Git clone (if gitRepoService provided)
     *
     * Returns null if config cannot be loaded.
     */
    fun loadWorkspaceConfig(
        gitRepoService: GitRepoService?,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED
    ): WorkspaceConfig? {
        val wsConfigFile = findWorkspaceConfigInDir(WORKSPACE_REPO_DIR)
        if (wsConfigFile != null) {
            return Yaml.read(wsConfigFile, WorkspaceConfig::class)
        }
        if (gitRepoService == null) return null
        val repoInfo = gitRepoService.initRepo(
            GitRepoProps(
                WORKSPACE_REPO_DIR,
                WORKSPACE_REPO_URL,
                WORKSPACE_REPO_BRANCH,
                Duration.ofHours(6),
                "workspace",
                AuthType.NONE
            ),
            updatePolicy
        )
        val file = findWorkspaceConfigInDir(repoInfo.root) ?: return null
        return Yaml.read(file, WorkspaceConfig::class)
    }

    fun ensureDirs() {
        val dirs = listOf(
            HOME_DIR.resolve("conf"),
            TLS_DIR,
            DATA_DIR,
            ACME_DIR,
            BUNDLES_DIR,
            VOLUMES_DIR,
            SNAPSHOTS_DIR,
            WORKSPACE_REPO_DIR,
            RUNTIME_FILES_DIR,
            LOG_DIR
        )
        for (dir in dirs) {
            val file = dir.toFile()
            if (!file.exists() && !file.mkdirs() && !file.exists()) {
                error("Failed to create directory: $dir")
            }
        }
    }
}
