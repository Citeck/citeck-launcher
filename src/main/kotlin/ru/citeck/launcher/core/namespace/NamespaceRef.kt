package ru.citeck.launcher.core.namespace

data class NamespaceRef(
    val workspace: String,
    val namespace: String
) {

    override fun toString(): String {
        return "$workspace:$namespace"
    }
}
