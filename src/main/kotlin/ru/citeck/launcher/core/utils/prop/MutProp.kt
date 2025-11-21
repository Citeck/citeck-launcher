package ru.citeck.launcher.core.utils.prop

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.IdUtils
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock
import kotlin.reflect.KProperty

open class MutProp<T>(val name: String, value: T) {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val watchers = CopyOnWriteArrayList<(T, T) -> Unit>()
    private val lock = ReentrantLock()

    @Volatile
    private var changedAt: Long = System.currentTimeMillis()
    private val version = AtomicLong()

    constructor(value: T) : this(IdUtils.createStrId(), value)

    @Volatile
    private var value: T = value

    fun getValue(): T = value

    fun compareAndSet(expected: T, value: T): Long = lock.withLock {
        if (this.value == expected) {
            setValue(value) {}
        } else {
            version.get()
        }
    }

    fun setValue(value: T): Long {
        return setValue(value) {}
    }

    fun setValue(value: T, actionInLock: (Long) -> Unit): Long = lock.withLock {
        setValue(value, { error, before, after ->
            log.error(error) {
                "Exception while listener execution " +
                    "for change event of '$name': $before -> $after"
            }
        }, actionInLock)
    }

    fun setValue(
        value: T,
        onError: (Throwable, before: T, after: T) -> Unit,
        actionInLock: (Long) -> Unit = {}
    ): Long = lock.withLock {
        if (this.value == value) {
            return version.get()
        }
        logState { "Update $this: ${this.value} -> $value" }
        val valueBefore = this.value
        this.value = value
        for (listener in watchers) {
            try {
                listener(valueBefore, this.value)
            } catch (e: Throwable) {
                try {
                    onError(e, valueBefore, value)
                } catch (e: Throwable) {
                    this.value = valueBefore
                    throw e
                }
            }
        }
        changedAt = System.currentTimeMillis()
        version.incrementAndGet()
        actionInLock.invoke(version.get())
        version.get()
    }

    fun setValue(value: T, expectedVer: Long): Long = lock.withLock {
        if (expectedVer != version.get()) {
            return version.get()
        }
        setValue(value) {}
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
        return getValue()
    }

    operator fun setValue(thisRef: Any?, property: KProperty<*>, value: T) {
        setValue(value) {}
    }

    override fun toString(): String {
        return "MutProp($name)"
    }

    private inline fun logState(crossinline msg: () -> String) {
        log.trace { msg() }
    }
}
