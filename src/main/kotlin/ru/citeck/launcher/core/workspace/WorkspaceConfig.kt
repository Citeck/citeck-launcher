package ru.citeck.launcher.core.workspace

import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import ru.citeck.launcher.core.bundle.BundleRef
import ru.citeck.launcher.core.license.LicenseInstance
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.utils.json.serialization.TypedBlockImageDeserializer
import java.time.Duration

data class WorkspaceConfig(
    val quickStartVariants: List<QuickStartVariant> = emptyList(),
    val imageRepos: List<ImageRepo>,
    val bundleRepos: List<BundlesRepo>,
    val defaultWebappProps: NamespaceConfig.WebappProps = NamespaceConfig.WebappProps.DEFAULT,
    val webapps: List<AppConfig>,
    val postgres: PostgresProps = PostgresProps.DEFAULT,
    val keycloak: KeycloakProps = KeycloakProps.DEFAULT,
    val alfresco: AlfrescoProps = AlfrescoProps.DEFAULT,
    val onlyoffice: OnlyOfficeProps = OnlyOfficeProps.DEFAULT,
    val sttSidecar: SttSidecarProps = SttSidecarProps.DEFAULT,
    val qdrant: QdrantProps = QdrantProps.DEFAULT,
    val pgadmin: PgAdminProps = PgAdminProps.DEFAULT,
    val zookeeper: ZookeeperProps = ZookeeperProps.DEFAULT,
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

    class AlfrescoProps(
        val enabled: Boolean = false,
        val aliases: Set<String> = emptySet()
    ) {
        companion object {
            val DEFAULT = AlfrescoProps()
        }
    }

    // The image fields below accept a plain "repo:tag" string, a
    // {repository, tag} map, or a LIST of either — a list resolves to its
    // FIRST element, the same reading the 2.x launcher's `bundle.ImageRef`
    // gives the same field in the same file (see
    // TypedBlockImageDeserializer's doc comment and AGENTS.md rule (6)).
    // Without the annotation a sequence here is a plain Jackson type
    // mismatch on a `String` field, and it costs the WHOLE workspace config,
    // not just this one entry.

    class PostgresProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "postgres:17.5"
    ) {
        companion object {
            val DEFAULT = PostgresProps()
        }
    }

    class KeycloakProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "keycloak/keycloak:26.4.5"
    ) {
        companion object {
            val DEFAULT = KeycloakProps()
        }
    }
    class ZookeeperProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "zookeeper:3.9.4"
    ) {
        companion object {
            val DEFAULT = ZookeeperProps()
        }
    }

    class PgAdminProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "dpage/pgadmin4:9.10.0"
    ) {
        companion object {
            val DEFAULT = PgAdminProps()
        }
    }

    class OnlyOfficeProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "onlyoffice/documentserver:9.1.0.1",
        val memoryLimit: String = "3g"
    ) {
        companion object {
            val DEFAULT = OnlyOfficeProps()
        }
    }

    class SttSidecarProps(
        @param:JsonDeserialize(using = TypedBlockImageDeserializer::class)
        val image: String = "",
        val memoryLimit: String = "2g",
        val port: Int = 14080
    ) {
        companion object {
            val DEFAULT = SttSidecarProps()
        }
    }

    class QdrantProps(
        val memoryLimit: String = "1g",
        val grpcPort: Int = 6334
    ) {
        companion object {
            val DEFAULT = QdrantProps()
        }
    }

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

    data class QuickStartVariant(
        val name: String,
        val snapshot: String = "",
        val bundleRef: BundleRef = BundleRef.EMPTY,
        val template: String = ""
    )

    enum class ImageRepoAuth {
        BASIC
    }
}
