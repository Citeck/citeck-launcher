package ru.citeck.launcher.core.namespace.init

import com.fasterxml.jackson.annotation.JsonSubTypes
import com.fasterxml.jackson.annotation.JsonTypeInfo

@JsonTypeInfo(
    use = JsonTypeInfo.Id.NAME,
    include = JsonTypeInfo.As.PROPERTY,
    property = "type"
)
@JsonSubTypes(
    JsonSubTypes.Type(value = ExecShell::class, name = "exec-shell")
)
sealed class AppInitAction

class ExecShell(
    val command: String
) : AppInitAction()
