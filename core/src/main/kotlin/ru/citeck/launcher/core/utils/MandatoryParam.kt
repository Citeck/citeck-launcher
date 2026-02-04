package ru.citeck.launcher.core.utils

import java.util.*
import java.util.function.Function

object MandatoryParam {

    @JvmStatic
    fun <T> check(name: String, value: T?, validator: Function<T?, Boolean>) {
        require(validator.apply(value)) { "$name is a mandatory parameter" }
    }

    @JvmStatic
    fun check(name: String, value: Any?) {
        check(name, value, Function { obj: Any? -> Objects.nonNull(obj) })
    }

    @JvmStatic
    fun checkString(name: String, value: CharSequence?) {
        check(name, value, Function { cs: CharSequence? -> StringUtils.isNotBlank(cs) })
    }

    @JvmStatic
    fun checkCollection(name: String, value: Collection<*>) {
        check(name, value)
        require(value.isNotEmpty()) { "$name collection must contain at least one item" }
    }
}
