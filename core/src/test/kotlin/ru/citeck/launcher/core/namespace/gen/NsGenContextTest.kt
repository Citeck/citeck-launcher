package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NsGenContextTest {

    @Test
    fun proxyBaseUrlHttpDefaultPort() {
        val ctx = createContext(port = 80, host = "localhost", tlsEnabled = false)
        assertThat(ctx.proxyBaseUrl).isEqualTo("http://localhost")
    }

    @Test
    fun proxyBaseUrlHttpsDefaultPort() {
        val ctx = createContext(port = 443, host = "example.com", tlsEnabled = true)
        assertThat(ctx.proxyBaseUrl).isEqualTo("https://example.com")
    }

    @Test
    fun proxyBaseUrlHttpNonStandardPort() {
        val ctx = createContext(port = 8080, host = "localhost", tlsEnabled = false)
        assertThat(ctx.proxyBaseUrl).isEqualTo("http://localhost:8080")
    }

    @Test
    fun proxyBaseUrlHttpsNonStandardPort() {
        val ctx = createContext(port = 8443, host = "custom.launcher.ru", tlsEnabled = true)
        assertThat(ctx.proxyBaseUrl).isEqualTo("https://custom.launcher.ru:8443")
    }

    @Test
    fun proxyBaseUrlHttpPort443() {
        // HTTP on port 443 is non-standard for HTTP
        val ctx = createContext(port = 443, host = "localhost", tlsEnabled = false)
        assertThat(ctx.proxyBaseUrl).isEqualTo("http://localhost:443")
    }

    @Test
    fun proxyBaseUrlHttpsPort80() {
        // HTTPS on port 80 is non-standard for HTTPS
        val ctx = createContext(port = 80, host = "localhost", tlsEnabled = true)
        assertThat(ctx.proxyBaseUrl).isEqualTo("https://localhost:80")
    }

    @Test
    fun proxySchemeHttp() {
        val ctx = createContext(port = 80, host = "localhost", tlsEnabled = false)
        assertThat(ctx.proxyScheme).isEqualTo("http")
    }

    @Test
    fun proxySchemeHttps() {
        val ctx = createContext(port = 443, host = "localhost", tlsEnabled = true)
        assertThat(ctx.proxyScheme).isEqualTo("https")
    }

    @Test
    fun proxyHostDefault() {
        val ctx = createContext(port = 80, host = "", tlsEnabled = false)
        assertThat(ctx.proxyHost).isEqualTo("localhost")
    }

    @Test
    fun proxyHostCustom() {
        val ctx = createContext(port = 80, host = "my-host.example.com", tlsEnabled = false)
        assertThat(ctx.proxyHost).isEqualTo("my-host.example.com")
    }

    private fun createContext(port: Int, host: String, tlsEnabled: Boolean): NsGenContext {
        val proxyProps = NamespaceConfig.ProxyProps(
            port = port,
            host = host,
            tls = NamespaceConfig.TlsConfig(enabled = tlsEnabled)
        )
        val nsConfig = NamespaceConfig.Builder()
            .withProxy(proxyProps)
            .build()
        return NsGenContext(
            namespaceConfig = nsConfig,
            bundle = BundleDef.EMPTY,
            workspaceConfig = createMinimalWorkspaceConfig(),
            detachedApps = emptySet(),
            files = HashMap()
        )
    }

    private fun createMinimalWorkspaceConfig(): WorkspaceConfig {
        return WorkspaceConfig(
            imageRepos = emptyList(),
            bundleRepos = emptyList(),
            webapps = emptyList()
        )
    }
}
