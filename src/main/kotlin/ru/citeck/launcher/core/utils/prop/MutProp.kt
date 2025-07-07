package ru.citeck.launcher.core.utils.prop

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.Disposable
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock
import kotlin.reflect.KProperty

open class MutProp<T>(value: T) {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val listeners = CopyOnWriteArrayList<(T, T) -> Unit>()
    private val lock = ReentrantLock()

    var changedAt: Long = System.currentTimeMillis()
        private set

    @Volatile
    var value: T = value
        set(newValue) = lock.withLock {
            if (field == newValue) {
                return
            }
            val valueBefore = field
            field = newValue
            for (listener in listeners) {
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
        listeners.add(action)
        return object : Disposable {
            override fun dispose() {
                listeners.remove(action)
            }
        }
    }

    operator fun getValue(thisRef: Any?, property: KProperty<*>): T {
        return value
    }

    operator fun setValue(thisRef: Any?, property: KProperty<*>, value: T) {
        this.value = value
    }
}
