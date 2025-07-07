package ru.citeck.launcher.core.utils

import ru.citeck.launcher.core.utils.prop.MutProp

class ActionStatus(
    val message: String = "",
    val progress: Float = 0f // 0..1
) {
    companion object {
        fun of(message: String, progress: Float): ActionStatus {
            return ActionStatus(message, progress)
        }
    }

    val progressInPercent: Float
        get() = (progress * 10000).toInt() / 100f

    fun withMessage(message: String) = of(message, progress)
    fun withProgress(progress: Float) = of(message, progress)

    class Mut : MutProp<ActionStatus>(ActionStatus()) {

        var message: String
            set(value) { this.value = this.value.withMessage(value) }
            get() = this.value.message

        var progress: Float
            set(value) { this.value = this.value.withProgress(value) }
            get() = this.value.progress

        fun addProgress(amount: Float) {
            this.value = this.value.withProgress(progress + amount)
        }

        fun set(message: String, progress: Float) {
            this.value = ActionStatus(message, progress)
        }
    }
}
