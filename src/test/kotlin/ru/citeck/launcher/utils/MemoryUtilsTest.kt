package ru.citeck.launcher.utils

import org.junit.jupiter.api.Assertions
import org.junit.jupiter.api.Test
import ru.citeck.launcher.core.utils.MemoryUtils

class MemoryUtilsTest {

    @Test
    fun test() {
        Assertions.assertEquals(1 * 1024 * 1024L, MemoryUtils.parseMemAmountToBytes("1m"))
        Assertions.assertEquals((1.5 * 1024 * 1024L).toLong(), MemoryUtils.parseMemAmountToBytes("1.5m"))
        Assertions.assertEquals((1.5 * 1024 * 1024 * 1024).toLong(), MemoryUtils.parseMemAmountToBytes("1.5g"))
    }
}
