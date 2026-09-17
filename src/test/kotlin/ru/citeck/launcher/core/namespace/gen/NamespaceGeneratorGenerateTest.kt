package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceDto
import kotlin.test.Test

/**
 * The whole [NamespaceGenerator.generate] entry point, which every other test in
 * this package sidesteps by calling one generateXxx function on a hand-built
 * [NsGenContext].
 *
 * That is not the same thing, and the gap is not academic. generate() is where
 * the individual generators are ORDERED and where their results share one
 * context, so the properties below cannot be observed from any one of them:
 * whether a bundle app the workspace does not list is generated at all; whether
 * every dependsOn edge names an app that ended up in the result; whether two
 * generators published the same host port; whether qdrant survives the real
 * call path, which reaches it only after the webapp loop has produced rag.
 */
class NamespaceGeneratorGenerateTest {

    private val ragImage = "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"
    private val aiImage = "harbor.citeck.ru/enterprise/ecos-ai:1.0.0"
    private val eappsImage = "harbor.citeck.ru/ecos-eapps:1.0.0"
    private val gatewayImage = "harbor.citeck.ru/ecos-gateway:1.0.0"
    private val proxyImage = "harbor.citeck.ru/ecos-proxy:1.0.0"
    private val contentImage = "harbor.citeck.ru/ecos-content:1.0.0"

    // What generateQdrant pins. Spelled out here rather than read from the
    // generator so that the constant moving is a test failure and not a silent
    // agreement between two copies of the same mistake.
    private val pinnedQdrantImage = "qdrant/qdrant:v1.19.1"

    /**
     * generate() takes the workspace config from [WorkspaceServices], so the test
     * has to hand it a real one. Both service objects are inert until their init()
     * is called: every field of [LauncherServices] except the config server is
     * lazy, and the config server binds its port in init() rather than in its
     * constructor. Nothing below touches Docker, git or the database.
     */
    private fun generatorFor(wsConfig: WorkspaceConfig): NamespaceGenerator {
        val generator = NamespaceGenerator()
        generator.init(WorkspaceServices(LauncherServices(), WorkspaceDto.DEFAULT, wsConfig))
        return generator
    }

    private fun workspaceConfig(webapps: List<String>): WorkspaceConfig {
        return WorkspaceConfig(
            imageRepos = emptyList(),
            bundleRepos = emptyList(),
            webapps = webapps.map { WorkspaceConfig.AppConfig(it) }
        )
    }

    private fun bundle(apps: Map<String, String>): BundleDef {
        return BundleDef(
            key = BundleKey("1.0.0"),
            applications = apps.mapValues { BundleDef.BundleAppDef(it.value) },
            citeckApps = emptyList()
        )
    }

    /**
     * The shape a RAG-carrying enterprise bundle has. gateway and proxy are not
     * decoration: generateProxyApp reads the gateway app's SERVER_PORT with a
     * non-null assertion, so generate() throws on a bundle without them.
     */
    private fun ragBundle() = bundle(
        mapOf(
            AppName.GATEWAY to gatewayImage,
            AppName.PROXY to proxyImage,
            AppName.EAPPS to eappsImage,
            AppName.RAG to ragImage,
            AppName.AI to aiImage
        )
    )

    private fun generate(
        wsWebapps: List<String> = listOf(AppName.GATEWAY, AppName.EAPPS, AppName.RAG, AppName.AI),
        bundleDef: BundleDef = ragBundle(),
        detachedApps: Set<String> = emptySet()
    ): NamespaceGenResp {
        val wsConfig = workspaceConfig(wsWebapps)
        return generatorFor(wsConfig).generate(NamespaceConfig.DEFAULT, bundleDef, detachedApps)
    }

    private fun NamespaceGenResp.app(name: String): ApplicationDef? {
        return applications.find { it.name == name }
    }

    private fun NamespaceGenResp.names(): Set<String> {
        return applications.map { it.name }.toSet()
    }

    @Test
    fun `a rag bundle generates qdrant through the real entry point`() {
        val resp = generate()

        val qdrant = resp.app(AppName.QDRANT)
        assertThat(qdrant).describedAs("qdrant is reached only after the webapp loop emits rag").isNotNull
        // The two things this launcher's qdrant support consists of: a version
        // that comes from nowhere external, and the volume name 2.x uses for
        // generation 1. Both are asserted at the seam an operator actually runs,
        // not at the generateQdrant call that NamespaceGeneratorRagTest makes.
        assertThat(qdrant!!.image).isEqualTo(pinnedQdrantImage)
        assertThat(qdrant.volumes).contains("qdrant2:/qdrant/storage")
        // A bundle offering some other qdrant must not move it: with no
        // dependency gate here, an image handed over is applied straight onto
        // whatever the volume already holds.
        val withBundleQdrant = generate(
            bundleDef = bundle(
                mapOf(
                    AppName.GATEWAY to gatewayImage,
                    AppName.PROXY to proxyImage,
                    AppName.EAPPS to eappsImage,
                    AppName.RAG to ragImage,
                    AppName.AI to aiImage,
                    AppName.QDRANT to "qdrant/qdrant:v1.14.1"
                )
            )
        )
        assertThat(withBundleQdrant.app(AppName.QDRANT)!!.image).isEqualTo(pinnedQdrantImage)
    }

    @Test
    fun `every generated dependsOn names an app that is in the result`() {
        // Cross-generator by construction: rag's edge to qdrant is written by
        // generateQdrant, the webapp edges to zookeeper and rabbitmq by
        // generateWebapp, and nothing but generate() puts all of them in one
        // result. An edge to a missing app parks the dependent app forever.
        val resp = generate()
        val present = resp.names()

        val dangling = resp.applications.flatMap { app ->
            app.dependsOn.filter { !present.contains(it) }.map { "${app.name} -> $it" }
        }
        assertThat(dangling).isEmpty()
        // Guard against the assertion passing on an empty graph.
        assertThat(resp.app(AppName.RAG)!!.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `no two generated apps publish the same host port`() {
        // Also unobservable from a single generator: the host side of every
        // published binding is chosen independently by each of them, and a
        // collision makes the second container fail to start with a Docker error
        // that names a port and not the two apps that wanted it.
        val resp = generate()

        val owners = HashMap<String, MutableList<String>>()
        for (app in resp.applications) {
            for (port in app.ports) {
                // "17020:8080", "8025:8025/tcp" -- the host side is what collides.
                val host = port.substringBefore(":")
                owners.computeIfAbsent(host) { ArrayList() }.add(app.name)
            }
        }
        val collisions = owners.filterValues { it.size > 1 }
        assertThat(collisions).isEmpty()
        assertThat(owners).describedAs("nothing published means nothing to collide").isNotEmpty
    }

    @Test
    fun `a bundle app the workspace does not list is not generated`() {
        // The filter lives in generate()'s loop, not in generateWebapp, so this
        // is the only place it can be observed.
        val bundleDef = bundle(
            mapOf(
                AppName.GATEWAY to gatewayImage,
                AppName.PROXY to proxyImage,
                AppName.EAPPS to eappsImage,
                AppName.CONTENT to contentImage,
                AppName.RAG to ragImage
            )
        )
        val resp = generate(
            wsWebapps = listOf(AppName.GATEWAY, AppName.EAPPS, AppName.RAG),
            bundleDef = bundleDef
        )

        assertThat(resp.names()).contains(AppName.EAPPS, AppName.RAG)
        assertThat(resp.names()).doesNotContain(AppName.CONTENT)
    }

    @Test
    fun `detaching rag keeps qdrant in the result, auto-detached`() {
        val resp = generate(detachedApps = setOf(AppName.RAG))

        // The store stays described: stopping rag is how it gets run from an
        // IDE, and such a rag has to reach a qdrant on localhost. It does not
        // start by itself -- the runtime never starts an auto-detached app.
        assertThat(resp.names()).contains(AppName.QDRANT)
        assertThat(resp.autoDetachedApps).contains(AppName.QDRANT)
        assertThat(resp.app(AppName.RAG)!!.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `the response reports the apps whose detached state changes composition`() {
        // The daemon regenerates on a Start of any app in this set. rag is in it
        // precisely because qdrant's existence follows rag's attached state, as
        // the two tests above measure; losing the entry would leave a re-attached
        // rag without a vector store until something else forced a regeneration.
        val resp = generate()

        assertThat(resp.dependsOnDetachedApps).contains(AppName.RAG)
    }
}
