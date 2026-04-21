package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorKeycloakTest {

    private fun createContext(
        authType: NamespaceConfig.AuthenticationType = NamespaceConfig.AuthenticationType.BASIC
    ): NsGenContext {
        val nsConfig = NamespaceConfig.Builder()
            .withAuthentication(NamespaceConfig.AuthenticationProps(type = authType))
            .build()

        val context = NsGenContext(
            namespaceConfig = nsConfig,
            bundle = BundleDef(
                key = BundleKey("1.0.0"),
                applications = emptyMap(),
                citeckApps = emptyList()
            ),
            workspaceConfig = WorkspaceConfig(
                imageRepos = emptyList(),
                bundleRepos = emptyList(),
                webapps = emptyList()
            ),
            files = HashMap(),
            detachedApps = emptySet()
        )

        context.getOrCreateApp(AppName.POSTGRES)

        return context
    }

    private fun callGenerateKeycloak(context: NsGenContext) {
        val method = NamespaceGenerator::class.java.getDeclaredMethod("generateKeycloak", NsGenContext::class.java)
        method.isAccessible = true
        method.invoke(NamespaceGenerator(), context)
    }

    // --- BASIC auth (Keycloak disabled) ---

    @Test
    fun `basic auth - postgres gets keycloak db init script`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.BASIC)
        callGenerateKeycloak(context)

        val pg = context.applications[AppName.POSTGRES]!!.build(false)
        assertThat(pg.initActions).hasSize(1)
    }

    @Test
    fun `basic auth - keycloak app not created`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.BASIC)
        callGenerateKeycloak(context)

        assertThat(context.applications).doesNotContainKey(AppName.KEYCLOAK)
    }

    @Test
    fun `basic auth - no keycloak link added`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.BASIC)
        callGenerateKeycloak(context)

        assertThat(context.links).noneMatch { it.name == "Keycloak Admin" }
    }

    // --- KEYCLOAK auth ---

    @Test
    fun `keycloak auth - app created with admin credentials`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        assertThat(context.applications).containsKey(AppName.KEYCLOAK)
        val kk = context.applications[AppName.KEYCLOAK]!!.build(false)
        assertThat(kk.environments["KC_BOOTSTRAP_ADMIN_USERNAME"]).isEqualTo("admin")
        assertThat(kk.environments["KC_BOOTSTRAP_ADMIN_PASSWORD"]).isEqualTo("admin")
    }

    @Test
    fun `keycloak auth - depends on postgres`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        val kk = context.applications[AppName.KEYCLOAK]!!.build(false)
        assertThat(kk.dependsOn).contains(AppName.POSTGRES)
    }

    @Test
    fun `keycloak auth - has startup condition and memory limit`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        val kk = context.applications[AppName.KEYCLOAK]!!.build(false)
        assertThat(kk.startupConditions).isNotEmpty
        assertThat(kk.resources?.limits?.memory).isEqualTo("1g")
    }

    @Test
    fun `keycloak auth - realm import volume mounted`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        val kk = context.applications[AppName.KEYCLOAK]!!.build(false)
        assertThat(kk.volumes).anyMatch { it.contains("ecos-app-realm.json") }
        assertThat(kk.volumes).anyMatch { it.contains("healthcheck.sh") }
    }

    @Test
    fun `keycloak auth - cmd contains db connection and import realm`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        val kk = context.applications[AppName.KEYCLOAK]!!.build(false)
        assertThat(kk.cmd).isNotNull
        assertThat(kk.cmd!!).contains("start")
        assertThat(kk.cmd!!).contains("--import-realm")
        assertThat(kk.cmd!!).anyMatch { it.contains("jdbc:postgresql://${AppName.POSTGRES}") }
    }

    @Test
    fun `keycloak auth - postgres also gets init script`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        val pg = context.applications[AppName.POSTGRES]!!.build(false)
        assertThat(pg.initActions).hasSize(1)
    }

    @Test
    fun `keycloak auth - admin link added`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        callGenerateKeycloak(context)

        assertThat(context.links).anyMatch { it.name == "Keycloak Admin" }
        val link = context.links.first { it.name == "Keycloak Admin" }
        assertThat(link.url).contains("ecos-idp/auth")
    }
}
