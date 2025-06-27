package ru.citeck.launcher.core.workspace

import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.license.LicenseInstance
import ru.citeck.launcher.core.namespace.NamespaceDto
import java.time.Duration

data class WorkspaceConfig(
    val defaultBundleRef: BundleRef,
    val imageRepos: List<ImageRepo>,
    val bundleRepos: List<BundlesRepo>,
    val defaultWebappProps: NamespaceDto.WebappProps = NamespaceDto.WebappProps.DEFAULT,
    val webapps: List<AppConfig>,
    val licenses: List<LicenseInstance> = emptyList()
) {

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
        val defaultProps: NamespaceDto.WebappProps = NamespaceDto.WebappProps.DEFAULT
    )

    enum class ImageRepoAuth {
        BASIC
    }
}
