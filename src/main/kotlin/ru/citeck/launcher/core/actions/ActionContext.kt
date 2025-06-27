package ru.citeck.launcher.core.actions

class ActionContext<P : ActionParams<*>>(
    val params: P,
    var lastError: Throwable? = null,
    var retryIdx: Int = -1,
)
