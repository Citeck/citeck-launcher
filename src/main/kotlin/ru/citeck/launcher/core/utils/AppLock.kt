package ru.citeck.launcher.core.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.socket.AppLocalSocket
import java.io.RandomAccessFile
import java.nio.channels.OverlappingFileLockException

object AppLock {

    private const val APP_LOCK_FILE = "app.lock"

    private val log = KotlinLogging.logger {}

    fun tryToLock(): Boolean {
        return if (tryToLockImpl()) {
            true
        } else {
            doFallbackActions()
            false
        }
    }

    private fun doFallbackActions() {
        val lockFile = AppDir.PATH.resolve(APP_LOCK_FILE).toFile()
        val pidAndPort = try {
            lockFile.readLines().first().split("|").map { it.toLong() }
        } catch (e: Throwable) {
            null
        } ?: listOf(-1L, -1L)
        if (pidAndPort.size == 2 && pidAndPort[1] != -1L) {
            try {
                AppLocalSocket.sendCommand(pidAndPort[1].toInt(), AppLocalSocket.TakeFocusCommand, Unit::class)
            } catch (e: Throwable) {
                log.error(e) { "Command send failed" }
            }
        }
        log.warn { "Application already started. PID and port - $pidAndPort" }
    }

    private fun tryToLockImpl(): Boolean {
        return try {
            val lockFile = AppDir.PATH.resolve(APP_LOCK_FILE).toFile()
            val lockStream = RandomAccessFile(lockFile, "rw")
            val lockChannel = lockStream.channel
            val lock = lockChannel.tryLock()
            if (lock == null) {
                false
            } else {
                lockStream.setLength(0)
                lockStream.writeBytes(
                    ProcessHandle.current().pid().toString() + "|" + AppLocalSocket.run()
                )
                Runtime.getRuntime().addShutdownHook(Thread {
                    lock.release()
                    lockChannel.close()
                    lockFile.delete()
                })
                true
            }
        } catch (e: OverlappingFileLockException) {
            false
        } catch (e: Throwable) {
            log.error(e) { "Exception occurred while locking" }
            false
        }
    }
}
