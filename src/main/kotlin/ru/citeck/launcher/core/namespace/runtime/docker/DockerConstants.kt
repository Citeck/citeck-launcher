package ru.citeck.launcher.core.namespace.runtime.docker

import ru.citeck.launcher.core.namespace.NamespaceRef

object DockerConstants {

    const val NAME_DELIM = "_"

    fun getNameSuffix(namespaceRef: NamespaceRef): String {
        return NAME_DELIM +
            namespaceRef.namespace.lowercase() +
            NAME_DELIM +
            namespaceRef.workspace.lowercase()
    }

    fun getNamePrefix(namespaceRef: NamespaceRef): String {
        return "citeck$NAME_DELIM"
    }

    fun getDockerProjectName(namespaceRef: NamespaceRef): String {
        return "citeck${NAME_DELIM}launcher" + getNameSuffix(namespaceRef)
    }

    fun getVolumeName(srcName: String, namespaceRef: NamespaceRef): String {
        return getNamePrefix(namespaceRef) +
            "volume" +
            NAME_DELIM +
            srcName +
            getNameSuffix(namespaceRef)
    }
}
