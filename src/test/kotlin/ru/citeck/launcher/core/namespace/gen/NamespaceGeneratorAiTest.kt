package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.AI_IMAGE
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.AI_PORT
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.DEFAULT_STT_PORT
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.GATEWAY_PORT
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.STT_IMAGE
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorAiTest {

    private fun createContext(
        detachedApps: Set<String> = emptySet(),
        sttSidecarProps: WorkspaceConfig.SttSidecarProps = WorkspaceConfig.SttSidecarProps.DEFAULT,
        sttBundleImage: String? = STT_IMAGE,
        populateAiApp: Boolean = true
    ): NsGenContext {
        val bundleApps = mutableMapOf<String, BundleDef.BundleAppDef>(
            AppName.GATEWAY to BundleDef.BundleAppDef("gateway:latest"),
            AppName.AI to BundleDef.BundleAppDef(AI_IMAGE)
        )
        if (sttBundleImage != null) {
            bundleApps[AppName.STT_SIDECAR] = BundleDef.BundleAppDef(sttBundleImage)
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
                webapps = listOf(
                    WorkspaceConfig.AppConfig(AppName.GATEWAY),
                    WorkspaceConfig.AppConfig(AppName.AI)
                ),
                sttSidecar = sttSidecarProps
            ),
            files = HashMap(),
            detachedApps = detachedApps
        )

        context.getOrCreateApp(AppName.GATEWAY)
            .addEnv("SERVER_PORT", GATEWAY_PORT)

        if (populateAiApp) {
            context.getOrCreateApp(AppName.AI)
                .addEnv("SERVER_PORT", AI_PORT)
                .withImage(AI_IMAGE)
        }

        return context
    }

    // --- TC-5: AI and STT sidecar both active ---

    @Test
    fun `ai and stt-sidecar active - stt container created with correct config`() {
        val context = createContext()
        NamespaceGenerator().generateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.image).isEqualTo(STT_IMAGE)
        assertThat(stt.environments["PORT"]).isEqualTo(DEFAULT_STT_PORT.toString())
        assertThat(stt.resources?.limits?.memory).isEqualTo("2g")
        assertThat(stt.volumes).contains("stt_models:/root/.cache/gigaam")
        assertThat(stt.ports).containsExactly("$DEFAULT_STT_PORT:$DEFAULT_STT_PORT")

        val probe = stt.startupConditions.single().probe!!.http!!
        assertThat(probe.path).isEqualTo("/health")
        assertThat(probe.port).isEqualTo(DEFAULT_STT_PORT)
    }

    @Test
    fun `ai and stt-sidecar active - ai has stt url and depends on stt`() {
        val context = createContext()
        NamespaceGenerator().generateSttSidecar(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_CALLRECORDING_STT_SIDECARURL"])
            .isEqualTo("http://${AppName.STT_SIDECAR}:$DEFAULT_STT_PORT")
        assertThat(ai.dependsOn).contains(AppName.STT_SIDECAR)
    }

    @Test
    fun `ai and stt-sidecar active - proxy has ai target and depends on ai`() {
        val context = createContext()
        val gen = NamespaceGenerator()
        gen.generateSttSidecar(context)
        gen.generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["AI_TARGET"]).isEqualTo("${AppName.AI}:$AI_PORT")
        assertThat(proxy.dependsOn).contains(AppName.AI)
    }

    // --- TC-6: AI detached — STT cut out entirely (STT only serves AI) ---

    @Test
    fun `ai detached - stt-sidecar not created`() {
        val context = createContext(detachedApps = setOf(AppName.AI))
        NamespaceGenerator().generateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_CALLRECORDING_STT_SIDECARURL")
        assertThat(ai.dependsOn).doesNotContain(AppName.STT_SIDECAR)
    }

    @Test
    fun `ai detached - proxy has no ai target`() {
        val context = createContext(detachedApps = setOf(AppName.AI))
        val gen = NamespaceGenerator()
        gen.generateSttSidecar(context)
        gen.generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("AI_TARGET")
        assertThat(proxy.dependsOn).doesNotContain(AppName.AI)
    }

    // --- TC-7: AI active, STT sidecar detached ---

    @Test
    fun `stt detached - stt spec still created so user can re-attach`() {
        val context = createContext(detachedApps = setOf(AppName.STT_SIDECAR))
        NamespaceGenerator().generateSttSidecar(context)

        assertThat(context.applications).containsKey(AppName.STT_SIDECAR)
    }

    @Test
    fun `stt detached - ai has no stt url`() {
        val context = createContext(detachedApps = setOf(AppName.STT_SIDECAR))
        NamespaceGenerator().generateSttSidecar(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_CALLRECORDING_STT_SIDECARURL")
        assertThat(ai.dependsOn).doesNotContain(AppName.STT_SIDECAR)
    }

    @Test
    fun `stt detached - proxy still has ai target`() {
        val context = createContext(detachedApps = setOf(AppName.STT_SIDECAR))
        val gen = NamespaceGenerator()
        gen.generateSttSidecar(context)
        gen.generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["AI_TARGET"]).isEqualTo("${AppName.AI}:$AI_PORT")
        assertThat(proxy.dependsOn).contains(AppName.AI)
    }

    // --- TC-8: Custom memory limit ---

    @Test
    fun `stt-sidecar custom memory limit applied`() {
        val context = createContext(
            sttSidecarProps = WorkspaceConfig.SttSidecarProps(memoryLimit = "4g", port = DEFAULT_STT_PORT)
        )
        NamespaceGenerator().generateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.resources?.limits?.memory).isEqualTo("4g")
    }

    // --- Additional edge cases ---

    @Test
    fun `stt-sidecar custom port propagated to ai and container`() {
        val customPort = 9090
        val context = createContext(
            sttSidecarProps = WorkspaceConfig.SttSidecarProps(port = customPort)
        )
        NamespaceGenerator().generateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.environments["PORT"]).isEqualTo(customPort.toString())
        assertThat(stt.ports).containsExactly("$customPort:$customPort")

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_CALLRECORDING_STT_SIDECARURL"])
            .isEqualTo("http://${AppName.STT_SIDECAR}:$customPort")
    }

    @Test
    fun `no ai app in context - stt not created`() {
        val context = createContext(populateAiApp = false)
        NamespaceGenerator().generateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)
    }

    @Test
    fun `stt-sidecar not in bundle and no image in props - stt not created`() {
        val context = createContext(sttBundleImage = null)
        NamespaceGenerator().generateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_CALLRECORDING_STT_SIDECARURL")
    }

    @Test
    fun `stt-sidecar bundle image is blank and no image in props - stt not created`() {
        val context = createContext(sttBundleImage = "")
        NamespaceGenerator().generateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_CALLRECORDING_STT_SIDECARURL")
    }

    @Test
    fun `stt-sidecar image from props takes precedence over bundle`() {
        val propsImage = "custom-stt:2.0"
        val context = createContext(
            sttSidecarProps = WorkspaceConfig.SttSidecarProps(image = propsImage, port = DEFAULT_STT_PORT)
        )
        NamespaceGenerator().generateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.image).isEqualTo(propsImage)
    }

    @Test
    fun `proxy skips ai target when SERVER_PORT not set on ai app`() {
        val context = createContext(populateAiApp = false)
        context.getOrCreateApp(AppName.AI).withImage(AI_IMAGE)

        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("AI_TARGET")
        assertThat(proxy.dependsOn).doesNotContain(AppName.AI)
    }

    @Test
    fun `proxy skips ai target when SERVER_PORT is non-numeric`() {
        val context = createContext(populateAiApp = false)
        context.getOrCreateApp(AppName.AI)
            .addEnv("SERVER_PORT", "not-a-port")
            .withImage(AI_IMAGE)

        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("AI_TARGET")
        assertThat(proxy.dependsOn).doesNotContain(AppName.AI)
    }
}
