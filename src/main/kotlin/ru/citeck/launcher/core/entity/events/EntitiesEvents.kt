package ru.citeck.launcher.core.entity.events

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.reflect.KClass

class EntitiesEvents {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val entityCreatedListeners = ConcurrentHashMap<KClass<*>, MutableList<(EntityCreatedEvent<Any>) -> Any>>()
    private val entityUpdatedListeners = ConcurrentHashMap<KClass<*>, MutableList<(EntityUpdatedEvent<Any>) -> Any>>()
    private val entityDeletedListeners = ConcurrentHashMap<KClass<*>, MutableList<(EntityDeletedEvent<Any>) -> Any>>()

    fun <T : Any> addEntityCreatedListener(type: KClass<out T>, action: (EntityCreatedEvent<T>) -> Any): Disposable {
        @Suppress("UNCHECKED_CAST")
        action as ((EntityCreatedEvent<Any>) -> Unit)
        val listeners = entityCreatedListeners.computeIfAbsent(type) { CopyOnWriteArrayList() }
        listeners.add(action)
        return object : Disposable {
            override fun dispose() {
                listeners.remove(action)
            }
        }
    }

    fun <T : Any> addEntityDeletedListener(type: KClass<out T>, action: (EntityDeletedEvent<T>) -> Unit): Disposable {
        @Suppress("UNCHECKED_CAST")
        action as ((EntityDeletedEvent<Any>) -> Unit)
        val listeners = entityDeletedListeners.computeIfAbsent(type) { CopyOnWriteArrayList() }
        listeners.add(action)
        return object : Disposable {
            override fun dispose() {
                listeners.remove(action)
            }
        }
    }

    fun <T : Any> addEntityUpdatedListener(type: KClass<out T>, action: (EntityUpdatedEvent<T>) -> Unit): Disposable {
        @Suppress("UNCHECKED_CAST")
        action as ((EntityUpdatedEvent<Any>) -> Unit)
        val listeners = entityUpdatedListeners.computeIfAbsent(type) { CopyOnWriteArrayList() }
        listeners.add(action)
        return object : Disposable {
            override fun dispose() {
                listeners.remove(action)
            }
        }
    }

    fun <T : Any> fireEntityCreatedEvent(type: KClass<T>, event: EntityCreatedEvent<T>): Promise<*> {
        return fireEvent(entityCreatedListeners, type, event)
    }

    fun <T : Any> fireEntityUpdatedEvent(type: KClass<T>, event: EntityUpdatedEvent<T>): Promise<*> {
        return fireEvent(entityUpdatedListeners, type, event)
    }

    fun <T : Any> fireEntityDeletedEvent(type: KClass<T>, event: EntityDeletedEvent<T>): Promise<*> {
        return fireEvent(entityDeletedListeners, type, event)
    }

    private fun <T : Any> fireEvent(
        listeners: Map<KClass<*>, List<(T) -> Any>>,
        type: KClass<*>,
        event: Event
    ): Promise<*> {
        log.info { "Fire ${event::class.simpleName} for type ${type.simpleName}" }
        @Suppress("UNCHECKED_CAST")
        event as T
        val promises = ArrayList<Promise<*>>()
        listeners[type]?.forEach {
            val res = it.invoke(event)
            if (res is Promise<*>) {
                promises.add(res)
            }
        }
        return if (promises.isNotEmpty()) {
            Promises.all(promises)
        } else {
            Promises.resolve(Unit)
        }
    }
}
