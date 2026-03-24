package ru.citeck.launcher.core.namespace

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.utils.json.Yaml
import kotlin.test.Test

class NamespaceConfigTest {

    @Test
    fun defaultConfig() {
        val config = NamespaceConfig.DEFAULT
        assertThat(config.id).isEmpty()
        assertThat(config.citeckProxy.port).isEqualTo(80)
        assertThat(config.citeckProxy.tls.enabled).isFalse()
        assertThat(config.authentication.type).isEqualTo(NamespaceConfig.AuthenticationType.BASIC)
    }

    @Test
    fun yamlDeserializationBasic() {
        val yaml = """
            id: "test"
            name: "Test NS"
            authentication:
              type: BASIC
              users:
                - "admin"
            proxy:
              port: 8080
              host: "myhost.com"
        """.trimIndent()

        val config = Yaml.read(yaml.byteInputStream(), NamespaceConfig::class)
        assertThat(config.id).isEqualTo("test")
        assertThat(config.name).isEqualTo("Test NS")
        assertThat(config.authentication.type).isEqualTo(NamespaceConfig.AuthenticationType.BASIC)
        assertThat(config.authentication.users).containsExactly("admin")
        assertThat(config.citeckProxy.port).isEqualTo(8080)
        assertThat(config.citeckProxy.host).isEqualTo("myhost.com")
        assertThat(config.citeckProxy.tls.enabled).isFalse()
    }

    @Test
    fun yamlDeserializationKeycloakTls() {
        val yaml = """
            id: "kc-test"
            name: "Keycloak TLS"
            authentication:
              type: KEYCLOAK
            proxy:
              port: 443
              host: "custom.launcher.ru"
              tls:
                enabled: true
                certPath: "/path/to/cert.crt"
                keyPath: "/path/to/key.key"
        """.trimIndent()

        val config = Yaml.read(yaml.byteInputStream(), NamespaceConfig::class)
        assertThat(config.id).isEqualTo("kc-test")
        assertThat(config.authentication.type).isEqualTo(NamespaceConfig.AuthenticationType.KEYCLOAK)
        assertThat(config.citeckProxy.port).isEqualTo(443)
        assertThat(config.citeckProxy.host).isEqualTo("custom.launcher.ru")
        assertThat(config.citeckProxy.tls.enabled).isTrue()
        assertThat(config.citeckProxy.tls.certPath).isEqualTo("/path/to/cert.crt")
        assertThat(config.citeckProxy.tls.keyPath).isEqualTo("/path/to/key.key")
    }

    @Test
    fun builderCopyRoundTrip() {
        val original = NamespaceConfig.Builder()
            .withId("ns1")
            .withName("Test")
            .withProxy(NamespaceConfig.ProxyProps(port = 8443, host = "example.com"))
            .build()

        val copy = original.copy().build()
        assertThat(copy.id).isEqualTo("ns1")
        assertThat(copy.name).isEqualTo("Test")
        assertThat(copy.citeckProxy.port).isEqualTo(8443)
        assertThat(copy.citeckProxy.host).isEqualTo("example.com")
    }
}
