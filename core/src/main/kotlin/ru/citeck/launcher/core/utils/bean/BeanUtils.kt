package ru.citeck.launcher.core.utils.bean

import io.github.oshai.kotlinlogging.KotlinLogging
import java.beans.BeanInfo
import java.beans.IntrospectionException
import java.beans.Introspector
import java.beans.PropertyDescriptor
import java.lang.reflect.Method
import java.util.concurrent.ConcurrentHashMap
import kotlin.reflect.KClass
import kotlin.reflect.KType
import kotlin.reflect.full.starProjectedType

object BeanUtils {

    private val log = KotlinLogging.logger {}

    private val cache = ConcurrentHashMap<KType, BeanDesc>()

    @JvmStatic
    fun getBeanDesc(type: Class<*>): BeanDesc {
        return getBeanDesc(type.kotlin)
    }

    @JvmStatic
    fun getBeanDesc(type: KClass<*>): BeanDesc {
        return getBeanDesc(type.starProjectedType)
    }

    @JvmStatic
    fun getBeanDesc(type: KType): BeanDesc {
        return cache.computeIfAbsent(type) { evalBeanDesc(it) }
    }

    @JvmStatic
    fun getProperties(type: Class<*>): List<PropertyDesc> {
        return getBeanDesc(type).getProperties()
    }

    @JvmStatic
    fun getProperties(type: KClass<*>): List<PropertyDesc> {
        return getBeanDesc(type).getProperties()
    }

    @JvmStatic
    fun getProperties(type: KType): List<PropertyDesc> {
        return getBeanDesc(type).getProperties()
    }

    @JvmStatic
    fun setProperty(bean: Any, name: String, value: Any?) {
        var beanIt = bean
        val nameParts = name.split(".")
        for ((idx, namePart) in nameParts.withIndex()) {
            if (idx == nameParts.lastIndex) {
                val beanDesc = getBeanDesc(bean::class)
                val property = beanDesc.getProperties().find {
                    it.getName() == namePart
                } ?: error(
                    "Property $name can't be set for bean $bean. " +
                        "Property with name '$namePart' doesn't found"
                )
                val writeMethod = property.getWriteMethod() ?: error(
                    "Property $name can't be set for bean $bean. " +
                        "Property with name $namePart is is not mutable."
                )
                writeMethod.isAccessible = true
                writeMethod.invoke(beanIt, value)
            } else {
                beanIt = getProperty(beanIt, namePart) ?: error(
                    "Property $name can't be set for bean $bean. " +
                        "Property $namePart return null"
                )
            }
        }
    }

    @JvmStatic
    fun getProperty(bean: Any, name: String): Any? {
        var beanIt = bean
        for (namePart in name.split(".")) {
            val beanDesc = getBeanDesc(bean::class)
            val property = beanDesc.getProperties().find { it.getName() == namePart } ?: return null
            val readMethod = property.getReadMethod() ?: return null
            readMethod.isAccessible = true
            beanIt = readMethod.invoke(beanIt) ?: return null
        }
        return beanIt
    }

    private fun evalBeanDesc(type: KType): BeanDesc {

        val beanInfo: BeanInfo?
        try {
            beanInfo = Introspector.getBeanInfo((type.classifier as KClass<*>).java)
        } catch (e: IntrospectionException) {
            // no descriptors are added to the context
            log.error(e) { "Error when inspecting class $type" }
            return BeanDescImpl(type, emptyList())
        }

        val descriptors = beanInfo.propertyDescriptors?.filter {
            it.name != "class"
        } ?: emptyList()

        return BeanDescImpl(type, descriptors)
    }

    private class BeanDescImpl(
        private val type: KType,
        properties: List<PropertyDescriptor>
    ) : BeanDesc {

        private val properties = properties.map { PropertyDescImpl(it) }

        override fun getBeanClass(): KClass<*> {
            return type.classifier as KClass<*>
        }

        override fun getType(): KType {
            return type
        }

        override fun getProperties(): List<PropertyDesc> {
            return properties
        }
    }

    private class PropertyDescImpl(
        private val descriptor: PropertyDescriptor
    ) : PropertyDesc {

        override fun getName(): String {
            return descriptor.name
        }

        override fun getPropType(): KType {
            return getPropClass().starProjectedType
        }

        override fun getPropClass(): KClass<*> {
            return descriptor.propertyType.kotlin
        }

        override fun getReadMethod(): Method? {
            return descriptor.readMethod
        }

        override fun getWriteMethod(): Method? {
            return descriptor.writeMethod
        }
    }
}
