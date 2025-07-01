package ru.citeck.launcher.core.utils

import java.time.Instant
import java.time.temporal.ChronoUnit

object StdOutLog {

    fun info(msg: String) {
        val time = Instant.now().truncatedTo(ChronoUnit.MILLIS).toString()
        println("${time.substring(0, time.length - 1)} [${Thread.currentThread().name}] INFO - $msg")
    }

    fun error(error: Throwable, msg: String) {
        this@StdOutLog.error(msg)
        error.printStackTrace()
    }

    fun error(msg: String) {
        val time = Instant.now().truncatedTo(ChronoUnit.MILLIS).toString()
        System.err.println("${time.substring(0, time.length - 1)} [${Thread.currentThread().name}] ERROR - $msg")
    }
}
