package ru.citeck.launcher.core.utils

import com.fasterxml.jackson.databind.type.TypeFactory
import ru.citeck.launcher.core.utils.json.Json
import java.lang.reflect.Type
import java.util.stream.Collectors
import kotlin.reflect.KClass

object ReflectUtils {

    @JvmStatic
    fun getGenericArg(type: KClass<*>, genericType: KClass<*>): KClass<*>? {
        val args = getGenericArgs(type, genericType)
        return if (args.isNotEmpty()) {
            args[0]
        } else {
            null
        }
    }

    @JvmStatic
    fun getGenericArgs(type: KClass<*>, genericType: KClass<*>): List<KClass<*>> {
        return getGenericTypeArgs(type, genericType)
            .stream()
            .map { t: Type? -> TypeFactory.rawClass(t).kotlin }
            .collect(Collectors.toList())
    }

    private fun getGenericTypeArgs(type: KClass<*>, genericType: KClass<*>): List<Type> {

        MandatoryParam.check("type", type)
        MandatoryParam.check("genericType", genericType)

        return if (type == genericType) {
            emptyList()
        } else {
            getGenericArguments(type.java, genericType.java)
        }
    }

    private fun getGenericArguments(type: Type?, genericType: Class<*>): List<Type> {

        if (type == null) {
            return emptyList()
        }

        return Json.mapper.typeFactory.constructType(type).findTypeParameters(genericType).toList()
    }
}
