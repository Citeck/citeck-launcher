package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorRagTest {

    private val ragImage = "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"
    private val qdrantImage = "qdrant/qdrant:v1.14.1"
    private val aiImage = "ai:latest"

    private fun createContext(
        detachedApps: Set<String> = emptySet(),
        withRagInBundle: Boolean = true,
        withQdrantInBundle: Boolean = true,
        withAiApp: Boolean = false,
        qdrantProps: WorkspaceConfig.QdrantProps = WorkspaceConfig.QdrantProps.DEFAULT
    ): NsGenContext {
        val bundleApps = mutableMapOf<String, BundleDef.BundleAppDef>()
        if (withRagInBundle) {
            bundleApps[AppName.RAG] = BundleDef.BundleAppDef(ragImage)
        }
        if (withQdrantInBundle) {
            bundleApps[AppName.QDRANT] = BundleDef.BundleAppDef(qdrantImage)
        }
        if (withAiApp) {
            bundleApps[AppName.AI] = BundleDef.BundleAppDef(aiImage)
        }

        val webapps = mutableListOf(WorkspaceConfig.AppConfig(AppName.RAG))
        if (withAiApp) {
            webapps.add(WorkspaceConfig.AppConfig(AppName.AI))
        }

        val context = NsGenContext(
            namespaceConfig = NamespaceConfig.DEFAULT,
            bundle = BundleDef(
                key = BundleKey("1.0.0"),
                applications = bundleApps,
                citeckApps = emptyList()
            ),
            workspaceConfig = WorkspaceConfig(
                imageRepos = emptyList(),
                bundleRepos = emptyList(),
                webapps = webapps,
                qdrant = qdrantProps
            ),
            files = HashMap(),
            detachedApps = detachedApps
        )
        if (withRagInBundle) {
            context.getOrCreateApp(AppName.RAG).withImage(ragImage)
        }
        if (withAiApp) {
            context.getOrCreateApp(AppName.AI).withImage(aiImage)
        }
        return context
    }

    @Test
    fun `qdrant is generated and wired when rag is active`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.image).isEqualTo(qdrantImage)
        assertThat(qdrant.volumes).contains("qdrant_storage:/qdrant/storage")

        val probe = qdrant.startupConditions.single().probe!!.http!!
        assertThat(probe.path).isEqualTo("/healthz")
        assertThat(probe.port).isEqualTo(6333)
        // AppStartAction.httpProbeCheck resolves the probe target ONLY from published
        // host-port bindings and has no container-IP fallback: without this port
        // published the probe returns false on every iteration, qdrant never becomes
        // ready, and rag waits on it until the failure threshold expires.
        assertThat(qdrant.ports).contains("6333:6333")
        // rag is told QDRANT_GRPC_PORT; the container has to be told the same thing or
        // a configured non-default port leaves rag dialling a socket qdrant never opened.
        assertThat(qdrant.environments["QDRANT__SERVICE__GRPC_PORT"]).isEqualTo("6334")

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("6334")
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `configured qdrant props reach both the container and rag`() {
        // The defect this guards is a CONFIGURED port: rag used to be told
        // QDRANT_GRPC_PORT while the container kept listening on the image default,
        // so a non-default value left rag dialling a socket qdrant never opened --
        // both apps RUNNING, every vector-store call dead. Asserting the default on
        // both sides would not catch that, since a hardcoded 6334 matches it.
        val context = createContext(
            qdrantProps = WorkspaceConfig.QdrantProps(memoryLimit = "2g", grpcPort = 7334)
        )
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.environments["QDRANT__SERVICE__GRPC_PORT"]).isEqualTo("7334")
        assertThat(qdrant.resources!!.limits.memory).isEqualTo("2g")

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("7334")
    }

    @Test
    fun `qdrant is not generated without rag in bundle`() {
        val context = createContext(withRagInBundle = false)
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.QDRANT)
    }

    @Test
    fun `qdrant is not generated when rag is detached`() {
        val context = createContext(detachedApps = setOf(AppName.RAG))
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.QDRANT)
    }

    @Test
    fun `rag to qdrant dependency is unconditional even when qdrant itself is detached`() {
        // Unlike ai -> stt-sidecar (optional: ai works without speech recognition),
        // rag cannot function without its vector store: a "running" rag without qdrant
        // is silently broken (no search, no indexing). So, unlike generateSttSidecar,
        // there is no guard here on context.detachedApps.contains(AppName.QDRANT) --
        // the dependency and env wiring must always be added when rag is active.
        val context = createContext(detachedApps = setOf(AppName.QDRANT))
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).containsKey(AppName.QDRANT)

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("6334")
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `qdrant is not generated when not present in bundle even with rag active`() {
        val context = createContext(withQdrantInBundle = false)
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.QDRANT)

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments).doesNotContainKey("QDRANT_HOST")
        assertThat(rag.dependsOn).doesNotContain(AppName.QDRANT)
    }

    @Test
    fun `ai gets rag enabled flag when ai is present and not detached`() {
        val context = createContext(withAiApp = true)
        NamespaceGenerator().generateQdrant(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_RAG_ENABLED"]).isEqualTo("true")
    }

    @Test
    fun `ai does not get rag enabled flag when ai is detached`() {
        val context = createContext(withAiApp = true, detachedApps = setOf(AppName.AI))
        NamespaceGenerator().generateQdrant(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_RAG_ENABLED")
    }

    @Test
    fun `no ai app in context - generation does not fail and rag still wired`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.AI)
        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `rag is marked as affecting namespace composition even when detached`() {
        // A Start on a detached rag must re-run generate() so that qdrant is (re)created once
        // rag is re-attached -- see NamespaceGenerator.DEPENDS_ON_DETACHED_APPS kdoc.
        assertThat(NamespaceGenerator.DEPENDS_ON_DETACHED_APPS).contains(AppName.RAG)
    }
}
