package ru.citeck.launcher.core.utils.bean

import kotlin.reflect.KClass
import kotlin.reflect.KType

interface BeanDesc {

    fun getBeanClass(): KClass<*>

    fun getType(): KType

    fun getProperties(): List<PropertyDesc>
}
