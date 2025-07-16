package ru.citeck.launcher.core.namespace

data class NamespaceRef(
    val workspace: String,
    val namespace: String
) {

    fun withWorkspace(workspace: String): NamespaceRef {
        return NamespaceRef(workspace, namespace)
    }

    override fun toString(): String {
        return "$workspace:$namespace"
    }
}
