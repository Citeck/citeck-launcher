package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.*
import org.assertj.core.api.SoftAssertions
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
    private fun readBundle(
        yaml: String,
        imageRepos: List<WorkspaceConfig.ImageRepo> = emptyList()
    ): BundleDef {
        val tempDir = createTempDirectory("bundle-def-test").toFile()
        File(tempDir, "bundle.yml").writeText(yaml)
        val workspaceConfig = WorkspaceConfig(
            imageRepos = imageRepos,
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

    // Same root cause as WorkspaceConfigImageListTest's typed-block coverage:
    // `Yaml.read(file, DataValue::class)` parses via SnakeYAML's `Load`,
    // which resolves an unquoted `tag: 17.10` to the Java Double 17.1 BEFORE
    // this code ever sees it - 17.10 and 17.1 are the same double, so nothing
    // downstream of that Double can recover the trailing zero. Measured
    // directly before this fix: `harbor/ecos-eapps:17.1`, not `...:17.10`.
    @Test
    fun `an unquoted tag with a trailing zero keeps its exact text - image map form`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                repository: harbor/ecos-eapps
                tag: 17.10
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:17.10")
    }

    @Test
    fun `an unquoted tag with a trailing zero keeps its exact text - first element of an image list`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                - repository: harbor/ecos-eapps
                  tag: 17.10
                - repository: harbor/ecos-eapps
                  tag: 18.6
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:17.10")
    }

    // ecosAppsImages goes through a separate repository/tag read than the
    // app's own `image:` (BundleUtils.readImage vs. the ecosAppsImages loop
    // in processApp) - covered separately so a fix to one cannot leave the
    // other still lossy.
    @Test
    fun `an unquoted tag with a trailing zero keeps its exact text - ecosAppsImages`() {
        val bundle = readBundle(
            """
            eapps:
              image: harbor/ecos-eapps:1.0.0
              ecosAppsImages:
                - repository: harbor/some-app
                  tag: 17.10
            """.trimIndent()
        )
        assertThat(bundle.citeckApps).containsExactly(BundleDef.BundleAppDef("harbor/some-app:17.10"))
    }

    @Test
    fun `a quoted tag is still read unchanged - image map form`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                repository: harbor/ecos-eapps
                tag: "17.9"
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:17.9")
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

    // Every row here was checked against the 2.x launcher's
    // resolveImageRefWithRepos (internal/bundle/resolver.go) by running the
    // same inputs through both parsers. The two that matter most are the
    // untagged ones ("busybox", "registry:5000/image"): Go does NOT treat
    // "no tag" as unreadable - it returns the string verbatim - and an
    // earlier version of this fix got that wrong, blanking the image and
    // silently dropping the app instead.
    private data class ImageRefCase(
        val label: String,
        val input: String,
        val expected: String,
        val imageRepos: List<WorkspaceConfig.ImageRepo> = emptyList()
    )

    private val imageRefParityCases = listOf(
        ImageRefCase(
            label = "a bare Docker Hub reference with no tag is passed through unchanged",
            input = "busybox",
            expected = "busybox"
        ),
        ImageRefCase(
            label = "a registry host:port with no tag is passed through unchanged",
            input = "registry:5000/image",
            expected = "registry:5000/image"
        ),
        ImageRefCase(
            label = "a registry host:port with a tag is passed through unchanged",
            input = "registry:5000/image:tag",
            expected = "registry:5000/image:tag"
        ),
        ImageRefCase(
            label = "repo/name:tag with an unmapped prefix is passed through unchanged",
            input = "repo/name:tag",
            expected = "repo/name:tag"
        ),
        ImageRefCase(
            label = "a digest reference with an unmapped prefix is passed through unchanged",
            input = "repo/name@sha256:abc123",
            expected = "repo/name@sha256:abc123"
        ),
        ImageRefCase(
            label = "a mapped prefix rewrites the registry and keeps the tag",
            input = "core/thing:1.2",
            expected = "nexus.citeck.ru/thing:1.2",
            imageRepos = listOf(WorkspaceConfig.ImageRepo("core", "nexus.citeck.ru"))
        ),
        ImageRefCase(
            label = "a mapped prefix rewrites the registry and keeps the digest",
            input = "core/thing@sha256:abc123",
            expected = "nexus.citeck.ru/thing@sha256:abc123",
            imageRepos = listOf(WorkspaceConfig.ImageRepo("core", "nexus.citeck.ru"))
        )
    )

    @Test
    fun `a scalar image matches the 2x launcher's registry-prefix rewrite for every shape`() {
        val softly = SoftAssertions()
        imageRefParityCases.forEach { case ->
            val bundle = readBundle(
                """
                eapps:
                  image: ${case.input}
                """.trimIndent(),
                case.imageRepos
            )
            softly.assertThat(bundle.applications["eapps"]?.image)
                .`as`(case.label)
                .isEqualTo(case.expected)
        }
        softly.assertAll()
    }

    @Test
    fun `the first element of an image list matches the 2x launcher's registry-prefix rewrite for every shape`() {
        val softly = SoftAssertions()
        imageRefParityCases.forEach { case ->
            val bundle = readBundle(
                """
                eapps:
                  image:
                    - ${case.input}
                    - some-other/unused-second-element:9.9.9
                """.trimIndent(),
                case.imageRepos
            )
            softly.assertThat(bundle.applications["eapps"]?.image)
                .`as`(case.label)
                .isEqualTo(case.expected)
        }
        softly.assertAll()
    }
}
