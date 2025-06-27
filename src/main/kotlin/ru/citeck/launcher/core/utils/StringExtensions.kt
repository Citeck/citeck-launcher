package ru.citeck.launcher.core.utils

private val camelRegex = "(?<=[a-zA-Z])[A-Z]".toRegex()
//private val snakeRegex = "_[a-zA-Z]".toRegex()

fun String.camelToSnakeCase(): String {
    return camelRegex.replace(this) {
        "_${it.value}"
    }.lowercase()
}
/*
fun String.snakeToLowerCamelCase(): String {
    return snakeRegex.replace(this) {
        it.value.replace("_","")
            .toUpperCase()
    }
}*/
