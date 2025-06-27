package ru.citeck.launcher.core.entity

class EntityActionDef(
    val id: String,
    val icon: String,
    val description: String,
    val action: suspend () -> Unit
)
