package ru.citeck.launcher.cli.commands

object CliUtils {

    fun isRoot(): Boolean = System.getProperty("user.name") == "root"

    fun detectFirewall(): String? {
        if (isCommandAvailable("ufw") && isServiceActive("ufw")) return "ufw"
        if (isCommandAvailable("firewall-cmd") && isServiceActive("firewalld")) return "firewalld"
        return null
    }

    fun addFirewallRule(firewall: String, port: Int) {
        when (firewall) {
            "ufw" -> execSafe("ufw", "allow", "$port/tcp")
            "firewalld" -> execSafe("firewall-cmd", "--permanent", "--add-port=$port/tcp")
        }
    }

    fun removeFirewallRule(firewall: String, port: Int) {
        when (firewall) {
            "ufw" -> execSafe("ufw", "delete", "allow", "$port/tcp")
            "firewalld" -> execSafe("firewall-cmd", "--permanent", "--remove-port=$port/tcp")
        }
    }

    fun reloadFirewall(firewall: String) {
        if (firewall == "firewalld") {
            execSafe("firewall-cmd", "--reload")
        }
    }

    fun execSafe(vararg cmd: String) {
        val exitCode = ProcessBuilder(*cmd).inheritIO().start().waitFor()
        if (exitCode != 0) {
            System.err.println("Warning: command exited with code $exitCode: ${cmd.joinToString(" ")}")
        }
    }

    fun isCommandAvailable(cmd: String): Boolean {
        return try {
            ProcessBuilder("which", cmd)
                .redirectErrorStream(true)
                .start()
                .waitFor() == 0
        } catch (_: Exception) {
            false
        }
    }

    fun isServiceActive(service: String): Boolean {
        return try {
            ProcessBuilder("systemctl", "is-active", "--quiet", service)
                .redirectErrorStream(true)
                .start()
                .waitFor() == 0
        } catch (_: Exception) {
            false
        }
    }

    fun promptWithDefault(text: String, default: String): String {
        print("$text [$default]: ")
        val value = readlnOrNull()
        return if (value.isNullOrBlank()) default else value.trim()
    }
}
