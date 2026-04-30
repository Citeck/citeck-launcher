package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.GATEWAY_PORT
import ru.citeck.launcher.core.namespace.gen.NamespaceGeneratorTestFixture.shellCommands
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorProxyTest {

    private fun createContext(
        authType: NamespaceConfig.AuthenticationType = NamespaceConfig.AuthenticationType.BASIC,
        users: Set<String> = setOf("admin", "fet"),
        detachedApps: Set<String> = emptySet(),
        addAlfresco: Boolean = false
    ): NsGenContext {
        val nsConfig = NamespaceConfig.Builder()
            .withAuthentication(NamespaceConfig.AuthenticationProps(type = authType, users = users))
            .build()

        val wsConfig = WorkspaceConfig(
            imageRepos = emptyList(),
            bundleRepos = emptyList(),
            webapps = listOf(WorkspaceConfig.AppConfig(AppName.GATEWAY)),
            alfresco = WorkspaceConfig.AlfrescoProps(enabled = addAlfresco)
        )

        val context = NsGenContext(
            namespaceConfig = nsConfig,
            bundle = BundleDef(
                key = BundleKey("1.0.0"),
                applications = mapOf(
                    AppName.GATEWAY to BundleDef.BundleAppDef("gateway:latest"),
                    AppName.PROXY to BundleDef.BundleAppDef("proxy:latest")
                ),
                citeckApps = emptyList()
            ),
            workspaceConfig = wsConfig,
            files = HashMap(),
            detachedApps = detachedApps
        )

        context.getOrCreateApp(AppName.GATEWAY)
            .addEnv("SERVER_PORT", GATEWAY_PORT)

        if (addAlfresco) {
            context.getOrCreateApp(AppName.ALFRESCO)
                .withImage("alfresco:latest")
        }

        return context
    }

    // --- BASIC auth ---

    @Test
    fun `basic auth - users formatted as user colon user pairs`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        val authAccess = proxy.environments["BASIC_AUTH_ACCESS"]!!
        assertThat(authAccess).contains("admin:admin")
        assertThat(authAccess).contains("fet:fet")
    }

    @Test
    fun `basic auth - single user formatted correctly`() {
        val context = createContext(users = setOf("superadmin"))
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["BASIC_AUTH_ACCESS"]).isEqualTo("superadmin:superadmin")
    }

    @Test
    fun `basic auth - no oidc env vars present`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("ENABLE_OIDC_FULL_ACCESS")
        assertThat(proxy.environments).doesNotContainKey("EIS_TARGET")
        assertThat(proxy.environments).doesNotContainKey("CLIENT_ID")
        assertThat(proxy.environments).doesNotContainKey("REALM_ID")
    }

    // --- KEYCLOAK auth ---

    @Test
    fun `keycloak auth - oidc env vars set`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["ENABLE_OIDC_FULL_ACCESS"]).isEqualTo("true")
        assertThat(proxy.environments["EIS_TARGET"]).isEqualTo("${AppName.KEYCLOAK}:8080")
        assertThat(proxy.environments["CLIENT_ID"]).isEqualTo("ecos-proxy-app")
        assertThat(proxy.environments["REALM_ID"]).isEqualTo("ecos-app")
        assertThat(proxy.environments["EIS_SCHEME"]).isEqualTo("http")
        assertThat(proxy.environments["REDIRECT_LOGOUT_URI"]).isEqualTo("http://localhost")
    }

    @Test
    fun `keycloak auth - no basic auth env`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("BASIC_AUTH_ACCESS")
    }

    @Test
    fun `keycloak auth - lua oidc volume mounted`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.volumes).anyMatch { it.contains("lua_oidc_full_access.lua") }
    }

    @Test
    fun `keycloak auth - init actions for sed and nginx reload`() {
        val context = createContext(authType = NamespaceConfig.AuthenticationType.KEYCLOAK)
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        val commands = shellCommands(proxy.initActions)
        assertThat(commands).hasSize(2)
        assertThat(commands).anyMatch {
            it.startsWith("sed -i") &&
                it.contains("/ecos-idp/auth/") &&
                it.contains("rewrite") &&
                it.contains("http://keycloak:8080/auth/")
        }
        assertThat(commands).anyMatch { it == "nginx -s reload" }
    }

    // --- Alfresco conditional ---

    @Test
    fun `alfresco active - proxy target is alfresco`() {
        val context = createContext(addAlfresco = true)
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["PROXY_TARGET"]).isEqualTo("${AppName.ALFRESCO}:8080")
        assertThat(proxy.environments["ALFRESCO_ENABLED"]).isEqualTo("true")
        assertThat(proxy.dependsOn).contains(AppName.ALFRESCO)
    }

    @Test
    fun `alfresco not present - proxy target is gateway`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["PROXY_TARGET"]).isEqualTo("${AppName.GATEWAY}:$GATEWAY_PORT")
        assertThat(proxy.environments["ALFRESCO_ENABLED"]).isEqualTo("false")
        assertThat(proxy.dependsOn).doesNotContain(AppName.ALFRESCO)
    }

    @Test
    fun `alfresco detached - proxy target is gateway`() {
        val context = createContext(addAlfresco = true, detachedApps = setOf(AppName.ALFRESCO))
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["PROXY_TARGET"]).isEqualTo("${AppName.GATEWAY}:$GATEWAY_PORT")
        assertThat(proxy.environments["ALFRESCO_ENABLED"]).isEqualTo("false")
    }

    // --- OnlyOffice dependency ---

    @Test
    fun `onlyoffice active - proxy depends on onlyoffice`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments["ONLYOFFICE_TARGET"]).isEqualTo(AppName.ONLYOFFICE)
        assertThat(proxy.dependsOn).contains(AppName.ONLYOFFICE)
    }

    @Test
    fun `onlyoffice detached - proxy does not depend on onlyoffice`() {
        val context = createContext(detachedApps = setOf(AppName.ONLYOFFICE))
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.environments).doesNotContainKey("ONLYOFFICE_TARGET")
        assertThat(proxy.dependsOn).doesNotContain(AppName.ONLYOFFICE)
    }

    // --- Common proxy config ---

    @Test
    fun `proxy always has gateway dependency and core env vars`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.dependsOn).contains(AppName.GATEWAY)
        assertThat(proxy.environments["GATEWAY_TARGET"]).isEqualTo("${AppName.GATEWAY}:$GATEWAY_PORT")
        assertThat(proxy.environments["DEFAULT_LOCATION_V2"]).isEqualTo("true")
        assertThat(proxy.environments["ENABLE_LOGGING"]).isEqualTo("warn")
        assertThat(proxy.environments["ENABLE_SERVER_STATUS"]).isEqualTo("true")
        assertThat(proxy.environments["ECOS_PAGE_TITLE"]).isEqualTo("Citeck Launcher")
    }

    @Test
    fun `proxy has health check and resource limits`() {
        val context = createContext()
        NamespaceGenerator().generateProxyApp(context)

        val proxy = context.applications[AppName.PROXY]!!.build(false)
        assertThat(proxy.startupConditions).isNotEmpty
        assertThat(proxy.resources?.limits?.memory).isEqualTo("128m")
    }
}
