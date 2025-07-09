package ru.citeck.launcher.core.namespace.gen

import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.config.cloud.CloudConfig

class NamespaceGenResp(
    /**
     * Список приложений, которые будут доступны
     */
    val applications: List<ApplicationDef>,
    /**
     * This is READ ONLY files which can be attached to containers
     */
    val files: Map<String, ByteArray>,

    val cloudConfig: CloudConfig,

    val links: List<NamespaceLink>,

    val dependsOnDetachedApps: Set<String>
)
