package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.*
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.bundle.BundleUtils
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.io.File
import kotlin.io.path.createTempDirectory
import kotlin.test.Test

class BundleDefTest {

    @Test
    fun testEqualsMethod() {

        val bundleDef = BundleDef(
            BundleKey("2025.1-RC2"),
            mapOf("userv" to BundleDef.BundleAppDef("nexus.citeck.ru/ecos-uiserv:2.23.2-snapshot")),
            listOf(BundleDef.BundleAppDef("nexus.citeck.ru/ecos-contracts:1.21.2-snapshot")),
            DataValue.createObj().set("aa", "bb")
        )
        val json = DataValue.of(bundleDef)

        val bundleFromJson = json.getAsNotNull(BundleDef::class)

        assertThat(bundleFromJson).isEqualTo(bundleDef)
    }

    // Reads the given bundle YAML through the same BundleUtils entry point
    // production uses (BundleUtils.loadBundles), so the test exercises the
    // real parser rather than a hand-built BundleDef. A single file is
    // written into a fresh temp directory per call, so there is exactly one
    // bundle to read back. loadBundles drops a BundleDef whose applications
    // and citeckApps are both empty (that's how a bundle file with nothing
    // readable in it is already handled), so callers whose YAML resolves to
    // nothing get BundleDef.EMPTY back instead of null.
    private fun readBundle(yaml: String): BundleDef {
        val tempDir = createTempDirectory("bundle-def-test").toFile()
        File(tempDir, "bundle.yml").writeText(yaml)
        val workspaceConfig = WorkspaceConfig(
            imageRepos = emptyList(),
            bundleRepos = emptyList(),
            webapps = emptyList()
        )
        val bundles = BundleUtils.loadBundles(tempDir.toPath(), workspaceConfig)
        return bundles.firstOrNull() ?: BundleDef.EMPTY
    }

    // The 2.x launcher accepts a list of images and, outside the dependencies
    // section, takes the first element. This launcher reads the same files, so
    // a list must not leave it with an empty image — and the element it takes
    // has to be the same one, or the two launchers run different versions off
    // one volume.
    @Test
    fun `an image list takes its first element`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                - harbor/ecos-eapps:1.0.0
                - harbor/ecos-eapps:2.0.0
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `an image list of repository-tag maps takes its first element`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                - {repository: harbor/ecos-eapps, tag: "1.0.0"}
                - {repository: harbor/ecos-eapps, tag: "2.0.0"}
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `a plain string image is read`() {
        val bundle = readBundle(
            """
            eapps:
              image: harbor/ecos-eapps:1.0.0
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `an empty image list leaves the app out`() {
        val bundle = readBundle(
            """
            eapps:
              image: []
            """.trimIndent()
        )
        assertThat(bundle.applications).doesNotContainKey("eapps")
    }
}
