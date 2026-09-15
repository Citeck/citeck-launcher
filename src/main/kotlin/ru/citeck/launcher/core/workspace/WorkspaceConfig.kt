package ru.citeck.launcher.core.workspace

import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import org.snakeyaml.engine.v2.nodes.Node
import ru.citeck.launcher.core.bundle.BundleRef
import ru.citeck.launcher.core.license.LicenseInstance
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.core.utils.json.serialization.RawImageValues
import ru.citeck.launcher.core.utils.json.serialization.TypedBlockImageDeserializer
import java.io.File
import java.nio.file.Path
import java.time.Duration
import kotlin.io.path.readText

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

    companion object {

        /**
         * Reads a workspace config from YAML text, then corrects the six
         * typed blocks' `image` fields (`postgres`, `keycloak`, `zookeeper`,
         * `onlyoffice`, `pgadmin`, `sttSidecar`) against the RAW source text
         * instead of the object graph `Yaml.read` built.
         *
         * `Yaml.read(text, WorkspaceConfig::class)` alone gets these six
         * fields right for a plain string, a {repository, tag} map with a
         * QUOTED tag, and a list of either — but an UNQUOTED tag like
         * `17.10` has already become the Java `Double` 17.1 by the time
         * `TypedBlockImageDeserializer` (a Jackson deserializer — it only
         * ever sees a `JsonNode` built from SnakeYAML's already-`Construct`ed
         * object graph) runs, and 17.10 and 17.1 are the same double: no
         * amount of formatting recovers the trailing zero. `Yaml.composeNode`
         * stops before `Construct`, so `RawImageValues.decodeImageValues`
         * reads the same six fields a second time from a tree that still has
         * the operator's literal text — mirroring Go's `bundle.ImageRef` /
         * `decodeImageValues` (`internal/bundle/resolver.go`), which reads
         * from the raw `*yaml.Node` for exactly this reason. Both launchers
         * read the same workspace-v1.yml and must resolve the same tag out
         * of it, or one data volume ends up on two different image versions.
         *
         * A block the raw pass cannot find or cannot read (absent, an empty
         * list, a shape neither decoder knows) falls back to whatever
         * `Yaml.read`'s ordinary Jackson path already computed for it —
         * `TypedBlockImageDeserializer`'s own default/empty handling is
         * unchanged, this only ever REPLACES a value with a more precise
         * spelling of the exact same information, never invents one.
         */
        fun read(text: String): WorkspaceConfig {
            val config = Yaml.read(text, WorkspaceConfig::class)
            val root = Yaml.composeNode(text) ?: return config
            return config.copy(
                postgres = PostgresProps(rawTypedBlockImage(root, "postgres") ?: config.postgres.image),
                keycloak = KeycloakProps(rawTypedBlockImage(root, "keycloak") ?: config.keycloak.image),
                zookeeper = ZookeeperProps(rawTypedBlockImage(root, "zookeeper") ?: config.zookeeper.image),
                onlyoffice = OnlyOfficeProps(
                    rawTypedBlockImage(root, "onlyoffice") ?: config.onlyoffice.image,
                    config.onlyoffice.memoryLimit
                ),
                pgadmin = PgAdminProps(rawTypedBlockImage(root, "pgadmin") ?: config.pgadmin.image),
                sttSidecar = SttSidecarProps(
                    rawTypedBlockImage(root, "sttSidecar") ?: config.sttSidecar.image,
                    config.sttSidecar.memoryLimit,
                    config.sttSidecar.port
                )
            )
        }

        fun read(file: Path): WorkspaceConfig = read(file.readText())

        fun read(file: File): WorkspaceConfig = read(file.readText())

        /**
         * The first element of the given typed block's `image:` as its exact
         * source text, or null when the block is absent, its `image` key is
         * absent, or the shape is one `RawImageValues` cannot read (in which
         * case the caller falls back to the Jackson-based answer, which
         * already handles "absent" and "unreadable" correctly — this
         * function only needs to improve on the cases it CAN read).
         */
        private fun rawTypedBlockImage(root: Node, blockKey: String): String? {
            val block = RawImageValues.mappingChild(root, blockKey) ?: return null
            val imageNode = RawImageValues.mappingChild(block, "image") ?: return null
            return RawImageValues.decodeImageValues(imageNode).firstOrNull()
        }
    }

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
