package ru.citeck.launcher.core.workspace

import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.license.LicenseInstance
import ru.citeck.launcher.core.namespace.NamespaceConfig
import java.time.Duration

data class WorkspaceConfig(
    val fastStartVariants: List<FastStartVariant> = emptyList(),
    val imageRepos: List<ImageRepo>,
    val bundleRepos: List<BundlesRepo>,
    val defaultWebappProps: NamespaceConfig.WebappProps = NamespaceConfig.WebappProps.DEFAULT,
    val webapps: List<AppConfig>,
    val citeckProxy: CiteckProxy = CiteckProxy(),
    val licenses: List<LicenseInstance> = emptyList(),
    val snapshots: List<Snapshot> = emptyList(),
    val namespaceTemplates: List<NamespaceTemplate> = emptyList()
) {

    val defaultNsTemplate = namespaceTemplates.find { it.id == "default" } ?: NamespaceTemplate("", "")

    val webappsById = webapps.associateBy { it.id }
    val imageReposById = imageRepos.associateBy { it.id }
    val imageReposByHost = imageRepos.associateBy { it.url.substringBefore("/") }
    val bundleReposById = bundleRepos.associateBy { it.id }

    class BundlesRepo(
        val id: String,
        val name: String,
        val url: String,
        val branch: String = "main",
        val path: String = "",
        val pullPeriod: Duration = Duration.ofHours(1),
    )

    class ImageRepo(
        val id: String,
        val url: String,
        val authType: ImageRepoAuth? = null
    )

    class AppConfig(
        val id: String,
        val aliases: Set<String> = emptySet(),
        val defaultProps: NamespaceConfig.WebappProps = NamespaceConfig.WebappProps.DEFAULT
    )

    class CiteckProxy(
        val aliases: Set<String> = setOf("EcosProxyApp")
    )

    class Snapshot(
        val id: String,
        val name: String,
        val url: String,
        val size: String,
        val sha256: String
    )

    data class NamespaceTemplate(
        val id: String,
        val name: String = id,
        val config: NamespaceConfig = NamespaceConfig.DEFAULT,
        val detachedApps: Set<String> = emptySet(),
    )

    data class FastStartVariant(
        val name: String,
        val snapshot: String = "",
        val bundleRef: BundleRef = BundleRef.EMPTY,
        val template: String = ""
    )

    enum class ImageRepoAuth {
        BASIC
    }
}
