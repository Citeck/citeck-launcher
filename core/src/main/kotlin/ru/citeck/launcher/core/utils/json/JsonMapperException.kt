package ru.citeck.launcher.core.utils.json

import com.fasterxml.jackson.databind.JavaType

class JsonMapperException : RuntimeException {

    val value: Any?
    val type: JavaType

    constructor(value: Any?, type: JavaType, cause: Throwable) : super(cause) {
        this.value = value
        this.type = type
    }

    constructor(msg: String, value: Any?, type: JavaType) : super(msg) {
        this.value = value
        this.type = type
    }
}
