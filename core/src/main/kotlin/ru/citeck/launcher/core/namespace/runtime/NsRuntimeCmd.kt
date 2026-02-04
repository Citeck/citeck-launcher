package ru.citeck.launcher.core.namespace.runtime

sealed class NsRuntimeCmd

data class StartNsCmd(
    val forceUpdate: Boolean
) : NsRuntimeCmd()

data object StopNsCmd : NsRuntimeCmd()

data object RegenerateNsCmd : NsRuntimeCmd()
