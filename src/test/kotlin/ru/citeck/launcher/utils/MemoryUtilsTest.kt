package ru.citeck.launcher.utils

import org.assertj.core.api.Assertions
import ru.citeck.launcher.core.utils.MemoryUtils
import kotlin.test.Test

class MemoryUtilsTest {

    @Test
    fun test() {
        Assertions.assertThat(MemoryUtils.parseMemAmountToBytes("1m")).isEqualTo(1 * 1024 * 1024L)
        Assertions.assertThat(MemoryUtils.parseMemAmountToBytes("1.5m")).isEqualTo((1.5 * 1024 * 1024L).toLong())
        Assertions.assertThat(MemoryUtils.parseMemAmountToBytes("1.5g")).isEqualTo((1.5 * 1024 * 1024 * 1024).toLong())
    }
}
