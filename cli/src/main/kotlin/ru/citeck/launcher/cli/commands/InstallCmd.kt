package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import io.ktor.client.*
import io.ktor.client.engine.cio.*
import io.ktor.client.plugins.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.cli.acme.AcmeClient
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleRef
import ru.citeck.launcher.core.bundle.BundleUtils
import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.secrets.auth.AuthType
import ru.citeck.launcher.core.ui.HeadlessUiProvider
import ru.citeck.launcher.core.utils.TlsCertGenerator
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import kotlin.io.path.exists
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.writeText

class InstallCmd : CliktCommand(name = "install") {

    private val gitRepoService by lazy {
        GitRepoService(HeadlessUiProvider()).also {
            it.init(AuthSecretsService())
        }
    }

    override fun help(context: Context) = "Configure Citeck platform"

    override fun run() {
        ConfigPaths.ensureDirs()

        // Step 1: Create config files if needed
        val (port, tlsConfig) = createConfigFiles()

        // Step 2: Open firewall port
        openFirewallPort(port, tlsConfig)

        // Step 3: Install systemd service
        installSystemdService()

        echo("")
        echo("Configuration complete.")
        echo("")
        echo("Commands:")
        echo("  sudo systemctl start citeck  - start platform")
        echo("  sudo systemctl stop citeck   - stop platform")
        echo("  citeck status                - show status")
        echo("  citeck reload                - reload configuration")
        echo("  journalctl -u citeck -f      - view logs")
    }

    private fun createConfigFiles(): Pair<Int, NamespaceConfig.TlsConfig> {
        val nsFile = ConfigPaths.NAMESPACE_CONFIG

        if (nsFile.exists()) {
            echo("Configuration already exists, skipping...")
            val existing = Yaml.read(nsFile, NamespaceConfig::class)
            return Pair(existing.citeckProxy.port, existing.citeckProxy.tls)
        }

        echo("--- Configuration ---")
        echo("")

        echo("Loading workspace configuration...")
        val wsConfig = loadWorkspaceConfig()
        if (wsConfig == null) {
            echo("Workspace config not available. Bundle and snapshot selection will use manual input.")
        }
        echo("")

        val name = CliUtils.promptWithDefault("Display name", "Production")

        echo("")
        echo("Authentication type:")
        echo("  1) BASIC (default)")
        echo("  2) KEYCLOAK")
        val authChoice = CliUtils.promptWithDefault("Choose [1/2]", "1")
        val authType = if (authChoice == "2") {
            NamespaceConfig.AuthenticationType.KEYCLOAK
        } else {
            NamespaceConfig.AuthenticationType.BASIC
        }

        val users = if (authType == NamespaceConfig.AuthenticationType.BASIC) {
            val usersStr = CliUtils.promptWithDefault("Users (comma-separated)", "admin")
            usersStr.split(",").map { it.trim() }.filter { it.isNotBlank() }.toSet()
        } else {
            emptySet()
        }

        echo("")
        val hostname = promptHostname()

        // TLS configuration
        val tlsConfig = configureTls(hostname)

        echo("")
        val defaultPort = if (tlsConfig.enabled) 443 else 80
        val portStr = CliUtils.promptWithDefault("Server port", defaultPort.toString())
        var port = portStr.toIntOrNull() ?: defaultPort
        if (port !in 1..65535) {
            echo("Invalid port: $port. Using default $defaultPort.", err = true)
            port = defaultPort
        }

        echo("")
        val pgAdminEnabled = CliUtils.promptWithDefault("Enable PgAdmin? [Y/n]", "Y").uppercase().startsWith("Y")

        val bundleRef = selectBundle(wsConfig)
        val snapshotId = selectSnapshot(wsConfig)

        // Create namespace.yml
        val nsConfig = NamespaceConfig.Builder().apply {
            this.id = "default"
            this.name = name
            this.snapshot = snapshotId
            this.authentication = NamespaceConfig.AuthenticationProps(authType, users)
            this.pgAdmin = NamespaceConfig.PgAdminProps(pgAdminEnabled)
            this.bundleRef = bundleRef
            this.proxy = NamespaceConfig.ProxyProps(
                port = port,
                host = hostname,
                tls = tlsConfig
            )
        }.build()

        nsFile.writeText(Yaml.toString(nsConfig))

        echo("")
        echo("Configuration created:")
        echo("  $nsFile")

        return Pair(port, tlsConfig)
    }

    private fun loadWorkspaceConfig(): WorkspaceConfig? {
        return try {
            ConfigPaths.loadWorkspaceConfig(gitRepoService)
        } catch (e: Exception) {
            echo("Could not load workspace config: ${e.message}", err = true)
            null
        }
    }

    private fun selectBundle(wsConfig: WorkspaceConfig?): BundleRef {
        if (wsConfig == null || wsConfig.bundleRepos.isEmpty()) {
            echo("")
            val ref = CliUtils.promptWithDefault("Bundle", "community:LATEST")
            return BundleRef.valueOf(ref)
        }

        val repos = wsConfig.bundleRepos

        // Step 1: Select repo
        echo("")
        echo("Bundle repository:")
        val repo = if (repos.size == 1) {
            echo("  ${repos[0].name}")
            repos[0]
        } else {
            repos.forEachIndexed { i, r ->
                val marker = if (i == 0) " (default)" else ""
                echo("  ${i + 1}) ${r.name}$marker")
            }
            val choice = CliUtils.promptWithDefault("Choose [1-${repos.size}]", "1")
            val idx = (choice.toIntOrNull() ?: 1).coerceIn(1, repos.size) - 1
            repos[idx]
        }

        // Step 2: Load bundles
        val bundles = loadBundlesFromRepo(repo, wsConfig)
        if (bundles.isEmpty()) {
            echo("No bundles found in '${repo.name}'")
            return BundleRef.valueOf(CliUtils.promptWithDefault("Bundle", "${repo.id}:LATEST"))
        }

        // Step 3: Select version
        val maxShow = 10
        echo("")
        echo("Available versions (${repo.name}):")
        bundles.take(maxShow).forEachIndexed { i, b ->
            val marker = if (i == 0) " (latest)" else ""
            echo("  ${i + 1}) ${b.key}$marker")
        }
        if (bundles.size > maxShow) {
            echo("  ... and ${bundles.size - maxShow} more")
        }
        val choice = CliUtils.promptWithDefault("Choose [1-${minOf(bundles.size, maxShow)}]", "1")
        val idx = (choice.toIntOrNull() ?: 1).coerceIn(1, minOf(bundles.size, maxShow)) - 1
        return BundleRef.create(repo.id, bundles[idx].key.rawKey)
    }

    private fun loadBundlesFromRepo(
        repo: WorkspaceConfig.BundlesRepo,
        wsConfig: WorkspaceConfig
    ): List<BundleDef> {
        val repoDir = if (repo.url.isBlank()) {
            ConfigPaths.WORKSPACE_REPO_DIR
        } else {
            // Clone bundle repo
            echo("Fetching bundles from ${repo.url}...")
            try {
                val repoInfo = gitRepoService.initRepo(
                    GitRepoProps(
                        ConfigPaths.BUNDLES_DIR.resolve(repo.id),
                        repo.url,
                        repo.branch,
                        repo.pullPeriod,
                        "install:bundle-repo",
                        AuthType.NONE
                    ),
                    GitUpdatePolicy.ALLOWED
                )
                repoInfo.root
            } catch (e: Exception) {
                echo("Failed to fetch bundle repo: ${e.message}", err = true)
                return emptyList()
            }
        }
        val path = repo.path.removePrefix("/")
        return try {
            BundleUtils.loadBundles(repoDir.resolve(path), wsConfig)
        } catch (e: Exception) {
            echo("Failed to load bundles: ${e.message}", err = true)
            emptyList()
        }
    }

    private fun selectSnapshot(wsConfig: WorkspaceConfig?): String {
        val snapshots = wsConfig?.snapshots ?: emptyList()
        if (snapshots.isEmpty()) {
            return ""
        }

        echo("")
        echo("Data snapshot:")
        echo("  1) Clean install (default)")
        snapshots.forEachIndexed { i, s ->
            echo("  ${i + 2}) ${s.name} (${s.size})")
        }
        val choice = CliUtils.promptWithDefault("Choose [1-${snapshots.size + 1}]", "1")
        val idx = (choice.toIntOrNull() ?: 1).coerceIn(1, snapshots.size + 1)
        return if (idx == 1) "" else snapshots[idx - 2].id
    }

    private fun promptHostname(): String {
        echo("Server hostname:")
        echo("  1) localhost (default)")
        echo("  2) Auto-detect public IP")
        echo("  3) Enter manually")
        val choice = CliUtils.promptWithDefault("Choose [1/2/3]", "1")

        return when (choice) {
            "2" -> {
                echo("")
                echo("This will make a request to https://ifconfig.me to detect your public IP.")
                val allow = CliUtils.promptWithDefault("Allow? [Y/n]", "Y")
                if (!allow.uppercase().startsWith("Y")) {
                    return CliUtils.promptWithDefault("Server hostname", "localhost")
                }
                echo("Detecting public IP...")
                val ip = detectPublicIp()
                if (ip != null) {
                    echo("Detected IP: $ip")
                    val confirm = CliUtils.promptWithDefault("Use $ip as server hostname? [Y/n]", "Y")
                    if (confirm.uppercase().startsWith("Y")) {
                        ip
                    } else {
                        CliUtils.promptWithDefault("Server hostname", "localhost")
                    }
                } else {
                    echo("Failed to detect public IP.", err = true)
                    CliUtils.promptWithDefault("Server hostname", "localhost")
                }
            }
            "3" -> CliUtils.promptWithDefault("Server hostname", "localhost")
            else -> "localhost"
        }
    }

    private fun detectPublicIp(): String? {
        val client = HttpClient(CIO) {
            install(HttpTimeout) {
                requestTimeoutMillis = 5000
                connectTimeoutMillis = 5000
            }
        }
        return try {
            runBlocking {
                val response = client.get("https://ifconfig.me/ip")
                val body = response.bodyAsText().trim()
                body.ifBlank { null }
            }
        } catch (_: Exception) {
            null
        } finally {
            client.close()
        }
    }

    private fun configureTls(hostname: String): NamespaceConfig.TlsConfig {
        if (hostname == "localhost") {
            echo("")
            echo("HTTPS configuration:")
            echo("  1) Self-signed certificate (default)")
            echo("  2) Existing certificate files")
            val choice = CliUtils.promptWithDefault("Choose [1/2]", "1")
            return when (choice) {
                "2" -> configureExistingCert()
                else -> generateSelfSigned(hostname)
            }
        }

        // Non-localhost: Let's Encrypt is available, loop on failure
        while (true) {
            echo("")
            echo("HTTPS configuration:")
            echo("  1) Self-signed certificate (default)")
            echo("  2) Let's Encrypt (auto-renewal)")
            echo("  3) Existing certificate files")
            val choice = CliUtils.promptWithDefault("Choose [1/2/3]", "1")
            val result = when (choice) {
                "2" -> configureLetsEncrypt(hostname)
                "3" -> configureExistingCert()
                else -> generateSelfSigned(hostname)
            }
            if (result != null) return result
        }
    }

    private fun generateSelfSigned(hostname: String): NamespaceConfig.TlsConfig {
        val tlsDir = ConfigPaths.TLS_DIR
        echo("Generating self-signed certificate...")
        TlsCertGenerator.generate(tlsDir, hostname)
        echo("Certificate generated in $tlsDir")
        return NamespaceConfig.TlsConfig(
            enabled = true,
            certPath = tlsDir.resolve("server.crt").toAbsolutePath().toString(),
            keyPath = tlsDir.resolve("server.key").toAbsolutePath().toString(),
            letsEncrypt = false
        )
    }

    private fun configureLetsEncrypt(hostname: String): NamespaceConfig.TlsConfig? {
        echo("")
        if (AcmeClient.isIpAddress(hostname)) {
            echo("Note: IP address certificates are short-lived (~6 days) and will be renewed automatically.")
            echo("")
        }
        val email = CliUtils.promptWithDefault("Email for renewal notifications (optional, press Enter to skip)", "")

        echo("")
        echo("Port 80 must be reachable from the internet for domain verification.")

        val firewall = CliUtils.detectFirewall()
        val port80Opened = openFirewallPortTemporarily(firewall, 80)

        try {
            // Stage 1: Validate with Let's Encrypt staging API to catch config
            // issues (DNS, port accessibility) without risking production rate limits
            echo("Validating configuration with Let's Encrypt staging API...")

            val stagingResult = AcmeClient.validateWithStaging(
                hostname,
                email.ifBlank { null },
                ConfigPaths.ACME_DIR
            )

            if (!stagingResult.success) {
                echo("")
                echo("Let's Encrypt validation failed: ${stagingResult.error}", err = true)
                echo("")
                echo("Domain verification could not be completed. Common causes:")
                echo("  - DNS not pointing to this server")
                echo("  - Port 80 not reachable from the internet")
                echo("  - Firewall blocking incoming connections")
                echo("")
                echo("Please fix the issue and try again, or choose another certificate option.")
                return null
            }

            echo("Staging validation passed.")
            echo("")

            // Stage 2: Obtain the real certificate from production API
            echo("Requesting production certificate from Let's Encrypt...")

            val result = AcmeClient.obtainCertificate(
                hostname,
                email.ifBlank { null },
                ConfigPaths.TLS_DIR,
                ConfigPaths.ACME_DIR
            )

            if (result.success) {
                echo("Certificate obtained successfully.")
                return NamespaceConfig.TlsConfig(
                    enabled = true,
                    certPath = result.certPath,
                    keyPath = result.keyPath,
                    letsEncrypt = true
                )
            } else {
                echo("")
                echo("Let's Encrypt certificate request failed: ${result.error}", err = true)
                echo("")
                echo("Please try again or choose another certificate option.")
                return null
            }
        } finally {
            if (port80Opened) {
                closeFirewallPort(firewall, 80)
            }
        }
    }

    private fun configureExistingCert(): NamespaceConfig.TlsConfig {
        echo("")
        val certDir = CliUtils.promptWithDefault("Certificate directory", "/etc/ssl/citeck")
        val certDirPath = Paths.get(certDir)
        val certPath = findFileByExtension(certDirPath, ".crt")
            ?: findFileByExtension(certDirPath, ".pem")
        val keyPath = findFileByExtension(certDirPath, ".key")

        if (certPath == null || keyPath == null) {
            echo("Could not find .crt/.pem and .key files in $certDir", err = true)
            echo("Please provide paths manually:")
            val cert = CliUtils.promptWithDefault("Path to certificate (.crt)", "$certDir/server.crt")
            val key = CliUtils.promptWithDefault("Path to private key (.key)", "$certDir/server.key")
            return NamespaceConfig.TlsConfig(
                enabled = true,
                certPath = cert,
                keyPath = key,
                letsEncrypt = false
            )
        } else {
            echo("Found certificate: $certPath")
            echo("Found key: $keyPath")
            return NamespaceConfig.TlsConfig(
                enabled = true,
                certPath = certPath.toAbsolutePath().toString(),
                keyPath = keyPath.toAbsolutePath().toString(),
                letsEncrypt = false
            )
        }
    }

    private fun findFileByExtension(dir: Path, extension: String): Path? {
        if (!dir.exists()) return null
        return try {
            dir.listDirectoryEntries("*$extension").firstOrNull()
        } catch (_: Exception) {
            null
        }
    }

    private fun openFirewallPort(port: Int, tlsConfig: NamespaceConfig.TlsConfig) {
        val firewall = CliUtils.detectFirewall()
        if (firewall == null) {
            echo("")
            echo("No firewall detected (ufw/firewalld). Skipping firewall configuration.")
            return
        }

        // Collect ports to open: main port + port 80 for LE renewal if applicable
        val ports = mutableSetOf(port)
        val isLetsEncrypt = tlsConfig.letsEncrypt
        if (isLetsEncrypt && port != 80) {
            ports.add(80)
        }

        echo("")
        echo("Ports ${ports.joinToString(", ") { "$it/tcp" }} need to be opened in $firewall.")
        if (isLetsEncrypt && 80 in ports) {
            echo("Port 80 is required for automatic Let's Encrypt certificate renewal.")
        }
        val confirm = CliUtils.promptWithDefault("Open ports in $firewall? [Y/n]", "Y")
        if (!confirm.uppercase().startsWith("Y")) {
            echo("Skipping firewall configuration. Open ports manually if needed.")
            return
        }

        for (p in ports) {
            CliUtils.addFirewallRule(firewall, p)
            echo("Firewall rule added: $firewall allow $p/tcp")
        }

        CliUtils.reloadFirewall(firewall)
    }

    private fun openFirewallPortTemporarily(firewall: String?, port: Int): Boolean {
        if (firewall == null) return false
        CliUtils.addFirewallRule(firewall, port)
        CliUtils.reloadFirewall(firewall)
        echo("Temporarily opened port $port/tcp in $firewall.")
        return true
    }

    private fun closeFirewallPort(firewall: String?, port: Int) {
        if (firewall == null) return
        CliUtils.removeFirewallRule(firewall, port)
        CliUtils.reloadFirewall(firewall)
        echo("Closed port $port/tcp in $firewall.")
    }

    private fun installSystemdService() {
        if (!CliUtils.isRoot()) {
            echo("")
            echo("Skipping systemd service installation (not running as root).")
            echo("To install manually, create /etc/systemd/system/citeck.service")
            return
        }

        val serviceFile = Paths.get("/etc/systemd/system/citeck.service")
        if (Files.exists(serviceFile)) {
            echo("")
            echo("Systemd service already exists, skipping...")
            return
        }

        val citeckBin = resolveExecPath()

        val unit = """
            [Unit]
            Description=Citeck Platform
            After=network.target docker.service
            Requires=docker.service

            [Service]
            Type=simple
            ExecStart=$citeckBin start --foreground
            ExecStop=$citeckBin stop --shutdown
            Restart=on-failure
            RestartSec=10
            StandardOutput=journal
            StandardError=journal

            [Install]
            WantedBy=multi-user.target
        """.trimIndent() + "\n"

        serviceFile.writeText(unit)
        CliUtils.execSafe("systemctl", "daemon-reload")
        CliUtils.execSafe("systemctl", "enable", "citeck")

        echo("")
        echo("Systemd service installed and enabled.")
    }

    private fun resolveExecPath(): String {
        val symlink = Paths.get("/usr/local/bin/citeck")
        if (symlink.exists()) return symlink.toAbsolutePath().toString()

        val javaHome = System.getProperty("java.home")
        val classpath = System.getProperty("java.class.path")
        val citeckHome = ConfigPaths.HOME_DIR.toAbsolutePath()
        return "$javaHome/bin/java --enable-native-access=ALL-UNNAMED -Dciteck.home=$citeckHome -cp $classpath ru.citeck.launcher.cli.CliMainKt"
    }
}
