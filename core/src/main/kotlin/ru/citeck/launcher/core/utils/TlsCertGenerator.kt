package ru.citeck.launcher.core.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import java.nio.file.Path
import java.security.KeyStore
import java.security.PrivateKey
import java.util.Base64
import kotlin.io.path.deleteIfExists
import kotlin.io.path.inputStream
import kotlin.io.path.writeText

object TlsCertGenerator {

    private val log = KotlinLogging.logger {}

    fun generate(dir: Path, hostname: String) {
        dir.toFile().mkdirs()
        val p12 = dir.resolve("keystore.p12")

        val keytool = findKeytool()
        log.info { "Generating self-signed certificate for hostname: $hostname" }

        execKeytool(
            keytool, "-genkeypair",
            "-alias", "server",
            "-keyalg", "RSA",
            "-keysize", "2048",
            "-validity", "36500",
            "-storetype", "PKCS12",
            "-keystore", p12.toString(),
            "-storepass", "changeit",
            "-dname", "CN=$hostname",
            "-ext", "SAN=dns:$hostname"
        )

        val ks = KeyStore.getInstance("PKCS12")
        p12.inputStream().use { ks.load(it, "changeit".toCharArray()) }
        val key = ks.getKey("server", "changeit".toCharArray()) as PrivateKey
        val cert = ks.getCertificate("server")

        writePem(dir.resolve("server.key"), "PRIVATE KEY", key.encoded)
        writePem(dir.resolve("server.crt"), "CERTIFICATE", cert.encoded)

        p12.deleteIfExists()
        log.info { "Self-signed certificate generated in $dir" }
    }

    fun writePem(path: Path, label: String, encoded: ByteArray) {
        val base64 = Base64.getMimeEncoder(64, "\n".toByteArray())
            .encodeToString(encoded)
        path.writeText("-----BEGIN $label-----\n$base64\n-----END $label-----\n")
    }

    fun execKeytool(vararg cmd: String) {
        val process = ProcessBuilder(*cmd).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        val exitCode = process.waitFor()
        if (exitCode != 0) {
            error("keytool failed (exit code $exitCode): $output")
        }
    }

    fun findKeytool(): String {
        val javaHome = System.getProperty("java.home") ?: ""
        if (javaHome.isNotBlank()) {
            val keytool = Path.of(javaHome, "bin", "keytool")
            if (keytool.toFile().exists()) {
                return keytool.toString()
            }
        }
        return "keytool"
    }
}
