package ru.citeck.launcher.core.namespace.gen

class NamespaceLink(
    val url: String,
    val name: String,
    val description: String,
    val icon: String,
    val order: Float,
    val category: String? = null,
    val alwaysEnabled: Boolean = false
)
