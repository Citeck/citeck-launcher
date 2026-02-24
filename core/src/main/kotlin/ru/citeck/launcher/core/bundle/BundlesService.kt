package ru.citeck.launcher.core.bundle

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceContext
import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.ui.UiProvider
import java.nio.file.Path
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

class BundlesService(
    private val uiProvider: UiProvider
) {

    companion object {
        private val log = KotlinLogging.logger { }
    }

    private lateinit var services: WorkspaceContext
    private val lock = ReentrantLock()

    private val bundleRepos = ConcurrentHashMap<String, BundlesRepoInfo>()

    fun init(services: WorkspaceContext) {
        this.services = services
    }

    fun getLatestRepoBundle(repoId: String): BundleRef {
        return getRepoBundles(repoId).firstOrNull() ?: BundleRef.EMPTY
    }

    fun getRepoBundles(repoId: String, max: Int = Int.MAX_VALUE): List<BundleRef> {
        if (repoId.isBlank()) {
            return emptyList()
        }
        try {
            return getRepoInfo(repoId)
                .bundles
                .asSequence()
                .take(max)
                .map { BundleRef.create(repoId, it.key.rawKey) }
                .toList()
        } catch (e: Throwable) {
            log.error(e) { "getRepoBundles for '$repoId' failed'" }
            uiProvider.showError(e)
            return emptyList()
        }
    }

    fun updateBundlesRepo(repo: String, updatePolicy: GitUpdatePolicy = GitUpdatePolicy.REQUIRED) {
        if (repo.isBlank()) {
            return
        }
        getRepoInfo(repo, updatePolicy)
    }

    fun getBundleByRef(
        ref: BundleRef,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS
    ): BundleDef {
        return getRepoInfo(ref.repo, updatePolicy).bundlesByRawKey[ref.key] ?: throw BundleNotFoundException(ref)
    }

    private fun getRepoInfo(
        repoId: String,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS
    ): BundlesRepoInfo {
        return lock.withLock {
            if (updatePolicy == GitUpdatePolicy.REQUIRED) {
                bundleRepos.remove(repoId)
            }
            var result = bundleRepos.computeIfAbsent(repoId) {
                pullAndReadBundlesRepo(repoId, updatePolicy)
            }
            if (updatePolicy == GitUpdatePolicy.ALLOWED && System.currentTimeMillis() >= result.nextPlannedUpdateMs) {
                bundleRepos.remove(repoId)
                result = bundleRepos.computeIfAbsent(repoId) {
                    pullAndReadBundlesRepo(repoId, updatePolicy)
                }
            }
            result
        }
    }

    private fun pullAndReadBundlesRepo(
        repoId: String,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS
    ): BundlesRepoInfo {

        val workspaceConfig = services.workspaceConfig.getValue()
        val repo = workspaceConfig.bundleReposById[repoId]
            ?: error("Can't find repo with id '$repoId'")

        val repoRoot: Path
        val nextUpdateMs: Long

        if (repo.url.isBlank()) {
            // Bundles are in the workspace repo — no extra git clone
            repoRoot = services.workspaceRepoDir
            if (!repoRoot.toFile().isDirectory) {
                log.warn { "Workspace repo dir does not exist: $repoRoot. Bundles will be empty." }
            }
            nextUpdateMs = Long.MAX_VALUE
        } else {
            val repoInfo = services.gitRepoService.initRepo(
                GitRepoProps(
                    services.bundlesDir.resolve(repo.id),
                    repo.url,
                    repo.branch,
                    repo.pullPeriod,
                    services.repoAuthId,
                    services.workspace.authType
                ),
                updatePolicy
            )
            repoRoot = repoInfo.root
            nextUpdateMs = repoInfo.nextUpdateMs
        }

        val path = if (repo.path.startsWith("/")) repo.path.substring(1) else repo.path

        return BundlesRepoInfo(
            BundleUtils.loadBundles(
                repoRoot.resolve(path),
                workspaceConfig
            ).ifEmpty { listOf(BundleDef.EMPTY) },
            nextPlannedUpdateMs = nextUpdateMs
        )
    }

    class BundlesRepoInfo(
        val bundles: List<BundleDef>,
        val nextPlannedUpdateMs: Long,
        val bundlesByRawKey: Map<String, BundleDef> = bundles.associateBy { it.key.rawKey }
    )
}
