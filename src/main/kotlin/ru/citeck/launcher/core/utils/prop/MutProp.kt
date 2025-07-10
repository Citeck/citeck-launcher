package ru.citeck.launcher.core.utils.prop

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.IdUtils
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock
import kotlin.reflect.KProperty

open class MutProp<T>(val name: String, value: T) {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val watchers = CopyOnWriteArrayList<(T, T) -> Unit>()
    private val lock = ReentrantLock()

    var changedAt: Long = System.currentTimeMillis()
        private set

    constructor(value: T) : this(IdUtils.createStrId(), value)

    @Volatile
    var value: T = value
        set(newValue) = lock.withLock {
            if (field == newValue) {
                return
            }
            logState { "Update $this: $field -> $newValue" }
            val valueBefore = field
            field = newValue
            for (listener in watchers) {
                try {
                    listener(valueBefore, field)
                } catch (e: Throwable) {
                    log.error(e) {
                        "Exception while listener execution " +
                        "for change event: $valueBefore -> $newValue"
                    }
                }
            }
            changedAt = System.currentTimeMillis()
        }

    fun watch(action: (T, T) -> Unit): Disposable {
        logState { "Add watcher for $this - $action" }
        watchers.add(action)
        if (watchers.size > 30) {
            log.warn { "Watchers size is greater than 20. Looks like a leak" }
        }
        return object : Disposable {
            override fun dispose() {
                logState { "Remove watcher for $this - $action" }
                watchers.remove(action)
            }
        }
    }

    operator fun getValue(thisRef: Any?, property: KProperty<*>): T {
        return value
    }

    operator fun setValue(thisRef: Any?, property: KProperty<*>, value: T) {
        this.value = value
    }

    override fun toString(): String {
        return "MutProp($name)"
    }

    private inline fun logState(crossinline msg: () -> String) {
        log.trace { msg() }
    }
}
