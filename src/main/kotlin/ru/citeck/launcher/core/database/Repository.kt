package ru.citeck.launcher.core.database

interface Repository<K, T> {

    operator fun set(id: K, value: T)

    operator fun get(id: K): T?

    fun delete(id: K)

    fun find(max: Int): List<T>

    fun getFirst(): T?

    /**
     * If action return true, then iteration will be stopped
     */
    fun forEach(action: (K, T) -> Boolean)
}
