package ru.citeck.launcher.core.utils

import ru.citeck.launcher.core.utils.prop.MutProp

class ActionStatus(
    val message: String = "",
    val progress: Float = 0f
) {
    companion object {
        fun of(message: String, progress: Float): ActionStatus {
            return ActionStatus(message, progress)
        }
        private val currentStatus = ThreadLocal<Mut>()

        fun <T> doWithStatus(status: Mut = Mut(), action: (actionStatus: Mut) -> T): T {
            val statusBefore = currentStatus.get()
            currentStatus.set(status)
            try {
                return action.invoke(status)
            } finally {
                if (statusBefore == null) {
                    currentStatus.remove()
                } else {
                    currentStatus.set(statusBefore)
                }
            }
        }

        fun getCurrentStatus(): Mut {
            return currentStatus.get() ?: Mut()
        }
    }

    val progressInPercent: Float
        get() = (progress * 10000).toInt() / 100f

    fun withMessage(message: String) = of(message, progress)
    fun withProgress(progress: Float) = of(message, progress)

    class Mut : MutProp<ActionStatus>(ActionStatus()) {

        var subStatusWatcher = Disposable.NONE

        var message: String
            set(value) {
                this.setValue(this.getValue().withMessage(value))
            }
            get() = this.getValue().message

        var progress: Float
            set(value) {
                this.setValue(this.getValue().withProgress(value))
            }
            get() = this.getValue().progress

        fun subStatus(amount: Float): Mut {
            subStatusWatcher.dispose()
            val subStatus = Mut()
            val baseProgress = progress
            subStatusWatcher = subStatus.watch { _, newStatus ->
                set(newStatus.message, baseProgress + amount * newStatus.progress)
            }
            return subStatus
        }

        fun addProgress(amount: Float) {
            this.setValue(this.getValue().withProgress(progress + amount))
        }

        fun set(message: String, progress: Float) {
            this.setValue(ActionStatus(message, progress))
        }
    }
}
