package ru.citeck.launcher.core.utils.bean

import java.lang.reflect.Method
import kotlin.reflect.KClass
import kotlin.reflect.KType

interface PropertyDesc {

    fun getName(): String

    fun getPropType(): KType

    fun getPropClass(): KClass<*>

    fun getReadMethod(): Method?

    fun getWriteMethod(): Method?
}
