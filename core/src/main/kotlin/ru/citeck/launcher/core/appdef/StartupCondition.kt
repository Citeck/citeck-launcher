package ru.citeck.launcher.core.appdef

data class StartupCondition(
    val probe: AppProbeDef? = null,
    val log: LogStartupCondition? = null
)

class LogStartupCondition(
    val pattern: String,
    val timeoutSeconds: Int = 60
)
