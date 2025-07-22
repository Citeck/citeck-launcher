package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.*
import ru.citeck.launcher.core.utils.data.DataValue
import kotlin.test.Test

class BundleDefTest {

    @Test
    fun testEqualsMethod() {

        val bundleDef = BundleDef(
            BundleKey("2025.1-RC2"),
            mapOf("userv" to BundleDef.BundleAppDef("nexus.citeck.ru/ecos-uiserv:2.23.2-snapshot")),
            listOf(BundleDef.BundleAppDef("nexus.citeck.ru/ecos-contracts:1.21.2-snapshot"))
        )
        val json = DataValue.of(bundleDef)

        val bundleFromJson = json.getAsNotNull(BundleDef::class)

        assertThat(bundleFromJson).isEqualTo(bundleDef)
    }
}
