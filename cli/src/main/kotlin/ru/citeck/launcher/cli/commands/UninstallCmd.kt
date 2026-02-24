package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import ru.citeck.launcher.cli.client.DaemonClient
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.utils.json.Yaml
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import kotlin.io.path.exists

class UninstallCmd : CliktCommand(name = "uninstall") {

    override fun help(context: Context) = "Uninstall Citeck platform"

    private val serviceName = "citeck"
    private val serviceFile: Path = Paths.get("/etc/systemd/system/$serviceName.service")

    override fun run() {
        if (!CliUtils.isRoot()) {
            echo("This command requires root privileges. Run with sudo.", err = true)
            return
        }

        // Step 1: Stop namespace via daemon
        stopNamespace()

        // Step 2: Remove systemd service
        removeSystemdService()

        // Step 3: Close firewall ports
        promptCloseFirewallPorts()

        // Step 4: Ask about data cleanup
        promptDeleteData()

        // Step 5: Remove symlink
        removeSymlink()

        echo("")
        echo("Uninstall complete.")
    }

    private fun stopNamespace() {
        val client = DaemonClient.create()
        if (client == null || !client.isRunning()) {
            client?.close()
            return
        }
        echo("Stopping platform...")
        client.use {
            it.stopNamespace()
            it.shutdown()
        }
        // Give the daemon time to shut down
        Thread.sleep(2000)
    }

    private fun removeSystemdService() {
        if (!Files.exists(serviceFile)) {
            echo("Systemd service not found, skipping...")
            return
        }
        CliUtils.execSafe("systemctl", "stop", serviceName)
        CliUtils.execSafe("systemctl", "disable", serviceName)
        Files.deleteIfExists(serviceFile)
        CliUtils.execSafe("systemctl", "daemon-reload")
        echo("Systemd service removed.")
    }

    private fun promptCloseFirewallPorts() {
        val firewall = CliUtils.detectFirewall() ?: return

        val nsFile = ConfigPaths.NAMESPACE_CONFIG
        if (!nsFile.exists()) return

        val nsConfig = try {
            Yaml.read(nsFile, NamespaceConfig::class)
        } catch (_: Exception) {
            return
        }

        val ports = mutableSetOf(nsConfig.citeckProxy.port)
        val isLetsEncrypt = nsConfig.citeckProxy.tls.letsEncrypt
        if (isLetsEncrypt && nsConfig.citeckProxy.port != 80) {
            ports.add(80)
        }

        echo("")
        val confirm = CliUtils.promptWithDefault(
            "Close ports ${ports.joinToString(", ") { "$it/tcp" }} in $firewall? [Y/n]",
            "Y"
        )
        if (confirm.uppercase().startsWith("Y")) {
            for (p in ports) {
                CliUtils.removeFirewallRule(firewall, p)
            }
            CliUtils.reloadFirewall(firewall)
            echo("Firewall ports closed.")
        }
    }

    private fun promptDeleteData() {
        echo("")
        echo("Platform data directory: ${ConfigPaths.HOME_DIR}")
        echo("  1) Keep all data (default)")
        echo("  2) Delete configuration only (${ConfigPaths.HOME_DIR}/conf/)")
        echo("  3) Delete all platform data (${ConfigPaths.HOME_DIR}/)")
        val choice = CliUtils.promptWithDefault("Choose [1/2/3]", "1")

        when (choice) {
            "2" -> {
                val confDir = ConfigPaths.HOME_DIR.resolve("conf")
                echo("")
                echo("This will permanently delete: $confDir")
                val typed = CliUtils.promptWithDefault("Type 'delete config' to confirm", "")
                if (typed == "delete config") {
                    confDir.toFile().deleteRecursively()
                    echo("Configuration deleted.")
                } else {
                    echo("Confirmation did not match. Skipping deletion.")
                }
            }
            "3" -> {
                echo("")
                echo("This will permanently delete ALL platform data:")
                echo("  - Configuration files")
                echo("  - Workspace and bundle repos")
                echo("  - Snapshots")
                echo("  - Runtime state")
                echo("")
                echo("Docker volumes in ${ConfigPaths.VOLUMES_DIR} will also be removed.")
                echo("NOTE: Docker containers/volumes created by the platform are NOT removed.")
                echo("")
                val typed = CliUtils.promptWithDefault("Type 'delete all data' to confirm", "")
                if (typed == "delete all data") {
                    ConfigPaths.HOME_DIR.toFile().deleteRecursively()
                    echo("All platform data deleted.")
                } else {
                    echo("Confirmation did not match. Skipping deletion.")
                }
            }
        }
    }

    private fun removeSymlink() {
        val symlink = Paths.get("/usr/local/bin/citeck")
        if (Files.exists(symlink)) {
            Files.deleteIfExists(symlink)
            echo("Removed $symlink")
        }
    }
}
