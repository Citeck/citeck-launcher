package ru.citeck.launcher.core.utils

interface Disposable {

    companion object {
        val NONE = object : Disposable{
            override fun dispose() {}
        }
    }

    fun dispose()
}
