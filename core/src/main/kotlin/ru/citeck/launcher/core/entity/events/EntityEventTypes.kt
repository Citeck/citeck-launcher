package ru.citeck.launcher.core.entity.events

import java.util.*

abstract class Event {
    val id: UUID = UUID.randomUUID()
}

data class EntityCreatedEvent<T : Any>(
    val entity: T
) : Event()

data class EntityUpdatedEvent<T : Any>(
    val entity: T
) : Event()

data class EntityDeletedEvent<T : Any>(
    val entity: T
) : Event()
