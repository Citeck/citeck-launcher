package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleUtils
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.nio.file.Files
import java.nio.file.Path
import kotlin.io.path.writeText
import kotlin.test.Test

/**
 * The `dependencies:` section of a bundle.
 *
 * It exists because it is INVISIBLE to launchers that do not know it: a
 * third-party image moved into it stops reaching them, which is how one bundle
 * can raise an infra version for launchers that gate such a move while everyone
 * else keeps running what they run today.
 *
 * This launcher has to read it for one reason: qdrant. Its image comes from the
 * bundle and from nowhere else — there is no built-in fallback and no workspace
 * default — so a bundle that keeps qdrant in `dependencies:` would leave `rag`
 * running with no vector store at all, silently. postgres, rabbitmq and
 * onlyoffice are unaffected either way: this generator takes those from the
 * workspace config, never from the bundle.
 */
class BundleDependenciesSectionTest {

    private fun tmpDir(): Path = Files.createTempDirectory("bundle-deps-test")

    private fun emptyWorkspace() = WorkspaceConfig(
        imageRepos = emptyList(),
        bundleRepos = emptyList(),
        webapps = emptyList()
    )

    @Test
    fun `entries under dependencies are read like top-level ones`() {
        val dir = tmpDir()
        dir.resolve("2026.3-RC3.yaml").writeText(
            """
            EcosModelApp:
              image:
                repository: core/ecos-model
                tag: 2.42.1
            dependencies:
              qdrant:
                image:
                  repository: qdrant/qdrant
                  tag: v1.14.1
              postgres:
                image:
                  repository: postgres
                  tag: "18.6"
            """.trimIndent()
        )

        val bundle = BundleUtils.loadBundles(dir, emptyWorkspace()).single()

        assertThat(bundle.imageOf("qdrant")).isEqualTo("qdrant/qdrant:v1.14.1")
        assertThat(bundle.imageOf("postgres")).isEqualTo("postgres:18.6")
        // They land in `dependencies`, NOT in `applications`: isEmpty() has to
        // go on meaning "no Citeck application", which the last case pins.
        assertThat(bundle.applications).doesNotContainKeys("qdrant", "postgres")
        // …and the section itself is never an application of its own. Without
        // the branch that descends into it, `processApp("dependencies", …)`
        // finds no image and silently adds nothing — which is exactly the bug
        // this test is about — but a future change that mishandles the key must
        // not produce an app literally named "dependencies" either.
        assertThat(bundle.applications).doesNotContainKey("dependencies")
        assertThat(bundle.dependencies).doesNotContainKey("dependencies")
        assertThat(bundle.imageOf("EcosModelApp")).isEqualTo("core/ecos-model:2.42.1")
    }

    /**
     * The 2.x launcher accepts both spellings of `image:` inside this section —
     * the plain string the section was specified with and the map every
     * top-level entry uses — so a bundle written against it must not read
     * differently here. A string form silently ignored would be the same class
     * of failure as not reading the section at all.
     */
    @Test
    fun `the image may be a plain string`() {
        val dir = tmpDir()
        dir.resolve("2026.3-RC3.yaml").writeText(
            """
            EcosModelApp:
              image:
                repository: core/ecos-model
                tag: 2.42.1
            dependencies:
              qdrant:
                image: qdrant/qdrant:v1.15.5
            """.trimIndent()
        )

        val bundle = BundleUtils.loadBundles(dir, emptyWorkspace()).single()

        assertThat(bundle.imageOf("qdrant")).isEqualTo("qdrant/qdrant:v1.15.5")
    }

    /**
     * An entry the section names but this launcher has no use for is KEPT and
     * simply never asked for. Failing the bundle over it would defeat the point
     * of a section whose whole job is to carry things some launchers ignore.
     */
    @Test
    fun `an unknown dependency id costs nothing`() {
        val dir = tmpDir()
        dir.resolve("2026.3-RC3.yaml").writeText(
            """
            EcosModelApp:
              image:
                repository: core/ecos-model
                tag: 2.42.1
            dependencies:
              something-this-launcher-never-heard-of:
                image:
                  repository: vendor/thing
                  tag: "1.0"
            """.trimIndent()
        )

        val bundle = BundleUtils.loadBundles(dir, emptyWorkspace()).single()

        assertThat(bundle.isEmpty()).isFalse()
        assertThat(bundle.imageOf("EcosModelApp")).isEqualTo("core/ecos-model:2.42.1")
        assertThat(bundle.imageOf("something-this-launcher-never-heard-of")).isEqualTo("vendor/thing:1.0")
    }

    /**
     * A bundle may name the same image in BOTH places — the shape a transition
     * takes, where the top-level entry is left behind for older launchers while
     * the section carries what the newer one should run. The section wins, which
     * is the order the 2.x resolver applies; getting it backwards would make the
     * section decorative on exactly the bundles that need it most.
     */
    @Test
    fun `a dependencies entry outranks a top-level one of the same name`() {
        val dir = tmpDir()
        dir.resolve("2026.3-RC3.yaml").writeText(
            """
            EcosModelApp:
              image:
                repository: core/ecos-model
                tag: 2.42.1
            qdrant:
              image:
                repository: qdrant/qdrant
                tag: v1.14.1
            dependencies:
              qdrant:
                image:
                  repository: qdrant/qdrant
                  tag: v1.15.5
            """.trimIndent()
        )

        val bundle = BundleUtils.loadBundles(dir, emptyWorkspace()).single()

        assertThat(bundle.imageOf("qdrant")).isEqualTo("qdrant/qdrant:v1.15.5")
        // The top-level entry is still READ, so a launcher that ignores the
        // section keeps finding it; it is simply outranked.
        assertThat(bundle.applications["qdrant"]?.image).isEqualTo("qdrant/qdrant:v1.14.1")
    }

    /**
     * A bundle whose ONLY content is the dependencies section carries no Citeck
     * application, and `isEmpty()` has to keep saying so: it is what
     * loadBundles uses to skip such a file, and without it the namespace would
     * come up as third-party containers with none of the product in them —
     * which passes every probe and reports RUNNING. This is the whole reason
     * the section is stored apart from `applications` rather than merged into
     * it, exactly as the 2.x launcher stores it.
     */
    @Test
    fun `a dependencies-only bundle is still empty`() {
        val dir = tmpDir()
        dir.resolve("2026.3-RC3.yaml").writeText(
            """
            dependencies:
              postgres:
                image:
                  repository: postgres
                  tag: "18.6"
            """.trimIndent()
        )

        assertThat(BundleUtils.loadBundles(dir, emptyWorkspace())).isEmpty()
    }
}
