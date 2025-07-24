package ru.citeck.launcher.core.config.bundle

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.workspace.WorkspacesService
import ru.citeck.launcher.view.dialog.ErrorDialog
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

class BundlesService {

    companion object {
        private val log = KotlinLogging.logger { }
    }

    private lateinit var services: WorkspaceServices
    private val lock = ReentrantLock()

    private val bundleRepos = ConcurrentHashMap<String, BundlesRepoInfo>()

    fun init(services: WorkspaceServices) {
        this.services = services
    }

    fun getLatestRepoBundle(repoId: String): BundleRef {
        return getRepoBundles(repoId).lastOrNull() ?: BundleRef.EMPTY
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
            ErrorDialog.show(e)
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

        val repoInfo = services.gitRepoService.initRepo(
            GitRepoProps(
                WorkspacesService.getWorkspaceDir(services.workspace.id)
                    .resolve("bundles")
                    .resolve(repo.id),
                repo.url,
                repo.branch,
                repo.pullPeriod,
                WorkspacesService.getRepoAuthId(services.workspace.id),
                services.workspace.authType
            ),
            updatePolicy
        )
        val path = if (repo.path.startsWith("/")) repo.path.substring(1) else repo.path

        return BundlesRepoInfo(
            BundleUtils.loadBundles(
                repoInfo.root.resolve(path),
                workspaceConfig
            ).ifEmpty { listOf(BundleDef.EMPTY) },
            nextPlannedUpdateMs = repoInfo.nextUpdateMs
        )
    }

    class BundlesRepoInfo(
        val bundles: List<BundleDef>,
        val nextPlannedUpdateMs: Long,
        val bundlesByRawKey: Map<String, BundleDef> = bundles.associateBy { it.key.rawKey }
    )
}
