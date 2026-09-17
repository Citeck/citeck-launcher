package ru.citeck.launcher.core.namespace.gen

import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.config.cloud.CloudConfig

class NamespaceGenResp(
    /**
     * The apps that will be available
     */
    val applications: List<ApplicationDef>,
    /**
     * This is READ ONLY files which can be attached to containers
     */
    val files: Map<String, ByteArray>,

    val cloudConfig: CloudConfig,

    val links: List<NamespaceLink>,

    val dependsOnDetachedApps: Set<String>,

    /**
     * Apps that are generated but must not be started by the runtime itself: a
     * companion (qdrant, stt-sidecar) nobody is holding. An explicit start by
     * the operator overrides it -- see NamespaceRuntime.
     */
    val autoDetachedApps: Set<String> = emptySet()
)
