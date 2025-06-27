package ru.citeck.launcher.core.appdef

class AppResourcesDef(
    val limits: LimitsDef = LimitsDef()
) {

    class LimitsDef(
        val memory: String = ""
    )
}
