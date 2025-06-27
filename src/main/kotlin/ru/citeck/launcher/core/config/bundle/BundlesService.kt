package ru.citeck.launcher.core.config.bundle

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.workspace.WorkspacesService
import ru.citeck.launcher.view.dialog.GlobalErrorDialog
import java.util.concurrent.ConcurrentHashMap

class BundlesService {

    companion object {
        private val log = KotlinLogging.logger { }
    }

    private lateinit var services: WorkspaceServices

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
            GlobalErrorDialog.show(GlobalErrorDialog.Params(e) {})
            return emptyList()
        }
    }

    fun getBundleByRef(ref: BundleRef): BundleDef {
        return getRepoInfo(ref.repo).bundlesByRawKey[ref.key]
            ?: error("Bundle is not found by key '${ref.key}' in repo ${ref.repo}")
    }

    private fun getRepoInfo(repoId: String): BundlesRepoInfo {
        val repo = services.workspaceConfig.bundleReposById[repoId]
            ?: error("Can't find repo with id '$repoId'")
        return bundleRepos.computeIfAbsent(repoId) {
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
                )
            )
            val path = if (repo.path.startsWith("/")) repo.path.substring(1) else repo.path

            BundlesRepoInfo(
                BundleUtils.loadBundles(
                    repoInfo.root.resolve(path),
                    services.workspaceConfig
                ).ifEmpty { listOf(BundleDef.EMPTY) }
            )
        }
    }

    class BundlesRepoInfo(
        val bundles: List<BundleDef>,
        val bundlesByRawKey: Map<String, BundleDef> = bundles.associateBy { it.key.rawKey }
    )
}
