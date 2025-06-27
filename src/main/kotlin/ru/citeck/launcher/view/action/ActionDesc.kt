package ru.citeck.launcher.view.action

class ActionDesc<T>(
    val id: String,
    val icon: ActionIcon,
    val description: String,
    val action: suspend (T) -> Any
)
