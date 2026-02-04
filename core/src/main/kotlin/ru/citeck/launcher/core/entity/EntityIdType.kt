package ru.citeck.launcher.core.entity

import kotlin.reflect.KClass

sealed class EntityIdType<T : Any>(val clazz: KClass<T>) {

    abstract fun isEmpty(id: T): Boolean

    abstract fun isValidId(id: T): Boolean

    data object Long : EntityIdType<kotlin.Long>(kotlin.Long::class) {

        override fun isEmpty(id: kotlin.Long): Boolean {
            return id == -1L
        }

        override fun isValidId(id: kotlin.Long): Boolean {
            return id >= 0
        }
    }

    data object String : EntityIdType<kotlin.String>(kotlin.String::class) {

        private val regex = "[\\w-.:$@]+".toRegex()

        override fun isEmpty(id: kotlin.String): Boolean {
            return id.isEmpty()
        }

        override fun isValidId(id: kotlin.String): Boolean {
            return id.isNotBlank() && id.matches(regex)
        }
    }
}
