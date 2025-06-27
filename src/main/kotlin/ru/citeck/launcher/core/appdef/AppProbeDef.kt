package ru.citeck.launcher.core.appdef

class AppProbeDef(
    val exec: ExecProbeDef? = null,
    val http: HttpProbeDef? = null,
    val initialDelaySeconds: Int = 5,
    val periodSeconds: Int = 10,
    val failureThreshold: Int = 10_000,
    val timeoutSeconds: Int = 5
)
class ExecProbeDef(
    val command: List<String>
)

class HttpProbeDef(
    val path: String,
    val port: Int
)
