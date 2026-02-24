package ru.citeck.launcher.cli.daemon

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.api.DaemonFiles
import ru.citeck.launcher.cli.acme.CertRenewalService
import ru.citeck.launcher.cli.daemon.server.DaemonServer
import ru.citeck.launcher.cli.daemon.services.DaemonServices
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.utils.AppLock
import java.nio.file.Paths
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.io.path.exists

class DaemonLifecycle {

    companion object {
        private val log = KotlinLogging.logger {}

        fun start() {
            DaemonLifecycle().run()
        }

        fun startBackground() {
            val javaHome = System.getProperty("java.home")
            val javaBin = "$javaHome/bin/java"
            val classpath = System.getProperty("java.class.path")
            val mainClass = "ru.citeck.launcher.cli.CliMainKt"
            val citeckHome = System.getProperty("citeck.home") ?: ConfigPaths.HOME_DIR.toString()

            val logFile = DaemonFiles.getLogFile().toFile()
            logFile.parentFile?.mkdirs()

            val cmd = mutableListOf(javaBin, "--enable-native-access=ALL-UNNAMED", "-Dciteck.home=$citeckHome", "-cp", classpath, mainClass, "start", "--foreground")
            val processBuilder = ProcessBuilder(cmd)
            processBuilder.redirectOutput(ProcessBuilder.Redirect.appendTo(logFile))
            processBuilder.redirectError(ProcessBuilder.Redirect.appendTo(logFile))

            val process = processBuilder.start()
            if (!process.isAlive) {
                throw IllegalStateException("Daemon process failed to start. Check logs: ${DaemonFiles.getLogFile()}")
            }
        }
    }

    private val shutdownLatch = CountDownLatch(1)
    private val shutdownStarted = AtomicBoolean(false)
    private lateinit var daemonServices: DaemonServices
    private lateinit var nsManager: NamespaceConfigManager
    private lateinit var server: DaemonServer
    private var certRenewalService: CertRenewalService? = null

    private fun run() {

        DaemonFiles.ensureDirs()

        if (!AppLock.tryToLock()) {
            error(
                "Failed to acquire application lock. " +
                    "Another instance of Citeck Launcher or Daemon is already running."
            )
        }

        // Register shutdown hook immediately after lock to ensure cleanup
        Runtime.getRuntime().addShutdownHook(
            Thread {
                log.info { "Shutdown hook triggered" }
                shutdown()
            }
        )

        try {
            daemonServices = DaemonServices()
            daemonServices.init()

            val workspaceContext = daemonServices.createWorkspaceContext()
            nsManager = NamespaceConfigManager(daemonServices, workspaceContext)
            val loaded = nsManager.load()

            if (loaded) {
                log.info { "Namespace loaded: ${nsManager.getConfig()?.id}" }
            } else {
                log.info { "No namespace configured. Create ${ConfigPaths.NAMESPACE_CONFIG}" }
            }

            server = DaemonServer(nsManager, ::shutdown)
            server.start()

            // Start certificate renewal service if using Let's Encrypt
            certRenewalService = createCertRenewalService(nsManager)
            certRenewalService?.start()

            val socketPath = DaemonFiles.getSocketFile()
            log.info { "Citeck Daemon started on $socketPath (PID: ${ProcessHandle.current().pid()})" }

            shutdownLatch.await()
        } catch (e: Throwable) {
            log.error(e) { "Daemon startup failed" }
            shutdown()
            throw e
        }
    }

    fun shutdown() {
        if (!shutdownStarted.compareAndSet(false, true)) {
            return
        }
        log.info { "Daemon shutdown requested" }
        try {
            certRenewalService?.dispose()
        } catch (e: Throwable) {
            log.error(e) { "Error disposing cert renewal service" }
        }
        try {
            if (::server.isInitialized) {
                server.stop()
            }
        } catch (e: Throwable) {
            log.error(e) { "Error stopping server" }
        }
        try {
            if (::nsManager.isInitialized) {
                nsManager.dispose()
            }
        } catch (e: Throwable) {
            log.error(e) { "Error disposing namespace manager" }
        }
        try {
            if (::daemonServices.isInitialized) {
                daemonServices.dispose()
            }
        } catch (e: Throwable) {
            log.error(e) { "Error disposing daemon services" }
        }
        shutdownLatch.countDown()
    }

    private fun createCertRenewalService(nsManager: NamespaceConfigManager): CertRenewalService? {
        val config = nsManager.getConfig() ?: return null
        val tls = config.citeckProxy.tls
        if (!tls.enabled || !tls.letsEncrypt || tls.certPath.isBlank()) return null

        if (!ConfigPaths.ACME_DIR.resolve("account-key.json").exists()) return null

        val certPath = Paths.get(tls.certPath)
        val targetDir = certPath.parent ?: return null

        return CertRenewalService(
            hostname = config.citeckProxy.host,
            certPath = certPath,
            targetDir = targetDir,
            acmeDir = ConfigPaths.ACME_DIR,
            onRenewed = { nsManager.reload() }
        )
    }
}
