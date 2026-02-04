package ru.citeck.launcher.core.entity

class ActionDesc<T>(
    val id: String,
    val icon: ActionIcon,
    val description: String,
    val action: suspend (T) -> Any
)
