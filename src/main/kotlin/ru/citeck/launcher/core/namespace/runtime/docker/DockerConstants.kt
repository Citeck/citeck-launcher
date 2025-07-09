package ru.citeck.launcher.core.namespace.runtime.docker

import ru.citeck.launcher.core.namespace.NamespaceRef

object DockerConstants {

    const val NAME_DELIM = "-"
    const val NAME_PREFIX = "citeck$NAME_DELIM"

    fun getNameSuffix(namespaceRef: NamespaceRef): String {
        return "$NAME_DELIM${namespaceRef.namespace}$NAME_DELIM${namespaceRef.workspace}"
    }

    fun getNamePrefix(namespaceRef: NamespaceRef): String {
        return NAME_PREFIX
    }

    fun getVolumeName(srcName: String, namespaceRef: NamespaceRef): String {
        return getNamePrefix(namespaceRef) + "volume" + NAME_DELIM + srcName + getNameSuffix(namespaceRef)
    }
}
