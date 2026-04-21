package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorAiTest {

    companion object {
        private const val AI_PORT = "8613"
        private const val GATEWAY_PORT = "8090"
        private const val DEFAULT_STT_PORT = 8090
        private const val STT_IMAGE = "stt-sidecar:latest"
        private const val AI_IMAGE = "ai:latest"
    }

    private fun createContext(
        detachedApps: Set<String> = emptySet(),
        sttSidecarProps: WorkspaceConfig.SttSidecarProps = WorkspaceConfig.SttSidecarProps.DEFAULT,
        includeSttInBundle: Boolean = true,
        populateAiApp: Boolean = true
    ): NsGenContext {
        val bundleApps = mutableMapOf<String, BundleDef.BundleAppDef>(
            AppName.GATEWAY to BundleDef.BundleAppDef("gateway:latest"),
            AppName.AI to BundleDef.BundleAppDef(AI_IMAGE)
        )
        if (includeSttInBundle) {
            bundleApps[AppName.STT_SIDECAR] = BundleDef.BundleAppDef(STT_IMAGE)
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

        if (populateAiApp && !detachedApps.contains(AppName.AI)) {
            context.getOrCreateApp(AppName.AI)
                .addEnv("SERVER_PORT", AI_PORT)
                .withImage(AI_IMAGE)
        }

        return context
    }

    private fun callGenerateSttSidecar(context: NsGenContext) {
        val method = NamespaceGenerator::class.java.getDeclaredMethod("generateSttSidecar", NsGenContext::class.java)
        method.isAccessible = true
        method.invoke(NamespaceGenerator(), context)
    }

    private fun callGenerateProxyApp(context: NsGenContext) {
        val method = NamespaceGenerator::class.java.getDeclaredMethod("generateProxyApp", NsGenContext::class.java)
        method.isAccessible = true
        method.invoke(NamespaceGenerator(), context)
    }

    // --- TC-5: AI and STT sidecar both active ---

    @Test
    fun `ai and stt-sidecar active - stt container created with correct config`() {
        val context = createContext()
        callGenerateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.image).isEqualTo(STT_IMAGE)
        assertThat(stt.environments["PORT"]).isEqualTo(DEFAULT_STT_PORT.toString())
        assertThat(stt.startupConditions).isNotEmpty
        assertThat(stt.resources?.limits?.memory).isEqualTo("2g")
    }

    @Test
    fun `ai and stt-sidecar active - ai has stt url and depends on stt`() {
        val context = createContext()
        callGenerateSttSidecar(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_CALLRECORDING_STT_SIDECARURL"])
            .isEqualTo("http://${AppName.STT_SIDECAR}:$DEFAULT_STT_PORT")
        assertThat(ai.dependsOn).contains(AppName.STT_SIDECAR)
    }

    @Test
    fun `ai and stt-sidecar active - proxy has ai target and depends on ai`() {
        val context = createContext()
        callGenerateSttSidecar(context)
        callGenerateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["AI_TARGET"]).isEqualTo("${AppName.AI}:$AI_PORT")
        assertThat(proxy.dependsOn).contains(AppName.AI)
    }

    // --- TC-6: AI detached ---

    @Test
    fun `ai detached - stt-sidecar not created`() {
        val context = createContext(detachedApps = setOf(AppName.AI), populateAiApp = false)
        callGenerateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)
    }

    @Test
    fun `ai detached - proxy has no ai target`() {
        val context = createContext(detachedApps = setOf(AppName.AI), populateAiApp = false)
        callGenerateSttSidecar(context)
        callGenerateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("AI_TARGET")
        assertThat(proxy.dependsOn).doesNotContain(AppName.AI)
    }

    // --- TC-7: AI active, STT sidecar detached ---

    @Test
    fun `stt detached - ai has no stt url`() {
        val context = createContext(detachedApps = setOf(AppName.STT_SIDECAR))
        callGenerateSttSidecar(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_CALLRECORDING_STT_SIDECARURL")
        assertThat(ai.dependsOn).doesNotContain(AppName.STT_SIDECAR)
    }

    @Test
    fun `stt detached - proxy still has ai target`() {
        val context = createContext(detachedApps = setOf(AppName.STT_SIDECAR))
        callGenerateSttSidecar(context)
        callGenerateProxyApp(context)

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
        callGenerateSttSidecar(context)

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
        callGenerateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.environments["PORT"]).isEqualTo(customPort.toString())

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_CALLRECORDING_STT_SIDECARURL"])
            .isEqualTo("http://${AppName.STT_SIDECAR}:$customPort")
    }

    @Test
    fun `stt-sidecar not in bundle and no image in props - stt not created`() {
        val context = createContext(includeSttInBundle = false)
        callGenerateSttSidecar(context)

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
        callGenerateSttSidecar(context)

        val stt = context.applications[AppName.STT_SIDECAR]!!.build(false)
        assertThat(stt.image).isEqualTo(propsImage)
    }

    @Test
    fun `both ai and stt detached - neither created, proxy clean`() {
        val context = createContext(
            detachedApps = setOf(AppName.AI, AppName.STT_SIDECAR),
            populateAiApp = false
        )
        callGenerateSttSidecar(context)
        callGenerateProxyApp(context)

        assertThat(context.applications).doesNotContainKey(AppName.STT_SIDECAR)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("AI_TARGET")
    }

    @Test
    fun `proxy uses default ai port when SERVER_PORT not set on ai app`() {
        val context = createContext(populateAiApp = false)
        context.getOrCreateApp(AppName.AI).withImage(AI_IMAGE)

        callGenerateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["AI_TARGET"]).isEqualTo("${AppName.AI}:8613")
    }
}
