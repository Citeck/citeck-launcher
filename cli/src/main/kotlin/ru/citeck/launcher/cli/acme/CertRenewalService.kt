package ru.citeck.launcher.cli.acme

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.Disposable
import java.io.FileInputStream
import java.nio.file.Path
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Duration
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

class CertRenewalService(
    private val hostname: String,
    private val certPath: Path,
    private val targetDir: Path,
    private val acmeDir: Path,
    private val onRenewed: () -> Unit
) : Disposable {

    companion object {
        private val log = KotlinLogging.logger {}
        private const val INITIAL_DELAY_MINUTES = 1L
        private const val CHECK_INTERVAL_HOURS = 12L
    }

    private val executor: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "cert-renewal").apply { isDaemon = true }
    }

    fun start() {
        executor.scheduleAtFixedRate(
            ::checkAndRenew,
            INITIAL_DELAY_MINUTES,
            CHECK_INTERVAL_HOURS * 60,
            TimeUnit.MINUTES
        )
        log.info { "Certificate renewal service started (checking every ${CHECK_INTERVAL_HOURS}h)" }
    }

    private fun checkAndRenew() {
        try {
            val certFile = certPath.toFile()
            if (!certFile.exists()) {
                log.warn { "Certificate file not found: $certPath" }
                return
            }

            val cf = CertificateFactory.getInstance("X.509")
            val cert = FileInputStream(certFile).use { cf.generateCertificate(it) } as X509Certificate
            val issued = cert.notBefore.toInstant()
            val expiry = cert.notAfter.toInstant()
            val remaining = Duration.between(Instant.now(), expiry)
            val totalValidity = Duration.between(issued, expiry)
            val threshold = totalValidity.dividedBy(2)

            if (remaining < threshold) {
                log.info { "Certificate expires in ${remaining.toDays()} days (threshold: ${threshold.toDays()} days), renewing..." }
                val result = AcmeClient.renewCertificate(hostname, targetDir, acmeDir)
                if (result.success) {
                    log.info { "Certificate renewed successfully" }
                    onRenewed()
                } else {
                    log.error { "Certificate renewal failed: ${result.error}" }
                }
            } else {
                log.debug { "Certificate valid for ${remaining.toDays()} more days (renewal after ${threshold.toDays()} days)" }
            }
        } catch (e: Exception) {
            log.error(e) { "Error checking certificate renewal" }
        }
    }

    override fun dispose() {
        executor.shutdownNow()
        log.info { "Certificate renewal service stopped" }
    }
}
