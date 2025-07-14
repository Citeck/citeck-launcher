package ru.citeck.launcher.core.namespace.gen

import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.config.cloud.CloudConfigImpl
import ru.citeck.launcher.core.config.cloud.MutableCloudConfig
import ru.citeck.launcher.core.config.bundle.BundleDef
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.util.concurrent.atomic.AtomicInteger

class NsGenContext(
    val namespaceConfig: NamespaceConfig,
    val bundle: BundleDef,
    val workspaceConfig: WorkspaceConfig,
    val detachedApps: Set<String>,
    val appFiles: MutableMap<String, ByteArray>,
    val applications: MutableMap<String, ApplicationDef.Builder> = LinkedHashMap(),
    val portsCounter: AtomicInteger = AtomicInteger(17020),
    val cloudConfig: MutableCloudConfig = CloudConfigImpl(),
    val links: MutableList<NamespaceLink> = ArrayList()
) {
    companion object {
        const val KK_HOST = AppName.KEYCLOAK

        const val PG_HOST = AppName.POSTGRES
        const val PG_PORT = 5432

        const val ZK_HOST = AppName.ZOOKEEPER
        const val ZK_PORT = 2181

        const val RMQ_HOST = AppName.RABBITMQ
        const val RMQ_PORT = 5672

        const val MONGO_HOST = AppName.MONGODB
        const val MONGO_PORT = 27017

        const val MAILHOG_HOST = AppName.MAILHOG
        const val ONLY_OFFICE_HOST = AppName.ONLY_OFFICE

        const val JWT_SECRET = "my-secret-key-which-should-be-changed-in-production-and-be-base64-encoded"

        val VARS = mapOf(
            "PG_HOST" to PG_HOST,
            "PG_PORT" to PG_PORT,
            "ZK_HOST" to ZK_HOST,
            "ZK_PORT" to ZK_PORT,
            "RMQ_HOST" to RMQ_HOST,
            "RMQ_PORT" to RMQ_PORT,
            "MONGO_HOST" to MONGO_HOST,
            "MONGO_PORT" to MONGO_PORT,
            "MAILHOG_HOST" to MAILHOG_HOST,
            "ONLY_OFFICE_HOST" to ONLY_OFFICE_HOST,
            "KK_ADMIN_URL" to "http://$KK_HOST:8080",
            "KK_ADMIN_USER" to "admin",
            "KK_ADMIN_PASSWORD" to "admin"
        )
    }

    fun getOrCreateApp(name: String): ApplicationDef.Builder {
        return applications.computeIfAbsent(name) {
            ApplicationDef.create(name)
        }
    }
}
