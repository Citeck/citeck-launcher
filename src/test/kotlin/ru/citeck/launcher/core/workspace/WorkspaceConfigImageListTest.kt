package ru.citeck.launcher.core.workspace

import org.assertj.core.api.Assertions.assertThat
import kotlin.test.Test

/**
 * Covers I1 of the whole-branch review: the 2.x launcher's `bundle.ImageRef`
 * now accepts a workspace-config typed block's `image:` as a LIST (taking
 * the first element), but these six blocks were still plain `String` fields
 * here — a shape that reads fine in Go throws a Jackson type-mismatch in
 * Kotlin, and (measured directly, before the fix in this commit) that
 * exception is not scoped to the one entry: `Yaml.read` throws before
 * `imageRepos`/`webapps`/anything else is parsed, so the WHOLE workspace
 * config fails to load and the workspace does not open at all.
 *
 * `TypedBlockImageDeserializer` fixes this by accepting the same three
 * shapes Go's `ImageRef` does (a plain string, a {repository, tag} map, and
 * a list of either) and resolving a list to its first element — the most
 * conservative rung, since this launcher has no dependency pin, no hold and
 * no migration to walk the rest of a ladder with.
 */
class WorkspaceConfigImageListTest {

    // Every WorkspaceConfig field required with no default, kept minimal so
    // each test's YAML only has to spell what it is actually testing.
    private val requiredFields = """
        imageRepos: []
        bundleRepos: []
        webapps: []
    """.trimIndent()

    // Goes through WorkspaceConfig.read, the same entry point production uses
    // (WorkspacesService.loadWorkspaceConfig) since the raw-text fixup for
    // the six typed blocks' `image` fields lives there, not in the generic
    // Yaml.read every other type still uses unchanged.
    private fun readConfig(extra: String): WorkspaceConfig {
        return WorkspaceConfig.read("$requiredFields\n$extra")
    }

    @Test
    fun `a plain string image is still read unchanged`() {
        val cfg = readConfig(
            """
            postgres:
              image: postgres:17.9
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    @Test
    fun `a postgres image list resolves to its first element`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                - postgres:17.9
                - postgres:18.6
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    @Test
    fun `an image list of repository-tag maps resolves to its first element`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                - {repository: postgres, tag: "17.9"}
                - {repository: postgres, tag: "18.6"}
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    @Test
    fun `an empty image list resolves to an empty string, not an exception`() {
        val cfg = readConfig(
            """
            postgres:
              image: []
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("")
    }

    @Test
    fun `every other field is still read when one typed block is a list`() {
        val cfg = readConfig(
            """
            keycloak:
              image: keycloak/keycloak:26.4.5
            postgres:
              image:
                - postgres:17.9
                - postgres:18.6
            """.trimIndent()
        )
        assertThat(cfg.keycloak.image).isEqualTo("keycloak/keycloak:26.4.5")
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    // All six typed blocks the review named go through the same
    // TypedBlockImageDeserializer. A per-field test each, so a future field
    // added to this set without the annotation fails here rather than
    // reopening this whole review finding.
    @Test
    fun `keycloak image list resolves to its first element`() {
        val cfg = readConfig(
            """
            keycloak:
              image:
                - keycloak/keycloak:26.4.5
                - keycloak/keycloak:27.0.0
            """.trimIndent()
        )
        assertThat(cfg.keycloak.image).isEqualTo("keycloak/keycloak:26.4.5")
    }

    @Test
    fun `zookeeper image list resolves to its first element`() {
        val cfg = readConfig(
            """
            zookeeper:
              image:
                - zookeeper:3.9.4
                - zookeeper:3.10.0
            """.trimIndent()
        )
        assertThat(cfg.zookeeper.image).isEqualTo("zookeeper:3.9.4")
    }

    @Test
    fun `onlyoffice image list resolves to its first element`() {
        val cfg = readConfig(
            """
            onlyoffice:
              image:
                - onlyoffice/documentserver:9.1.0.1
                - onlyoffice/documentserver:9.2.0.0
            """.trimIndent()
        )
        assertThat(cfg.onlyoffice.image).isEqualTo("onlyoffice/documentserver:9.1.0.1")
    }

    @Test
    fun `pgadmin image list resolves to its first element`() {
        val cfg = readConfig(
            """
            pgadmin:
              image:
                - dpage/pgadmin4:9.10.0
                - dpage/pgadmin4:9.11.0
            """.trimIndent()
        )
        assertThat(cfg.pgadmin.image).isEqualTo("dpage/pgadmin4:9.10.0")
    }

    @Test
    fun `sttSidecar image list resolves to its first element`() {
        val cfg = readConfig(
            """
            sttSidecar:
              image:
                - harbor/citeck/stt-sidecar:1.0.0
                - harbor/citeck/stt-sidecar:2.0.0
            """.trimIndent()
        )
        assertThat(cfg.sttSidecar.image).isEqualTo("harbor/citeck/stt-sidecar:1.0.0")
    }

    // Go's decodeImageValues (internal/bundle/resolver.go) reads a map's `tag`
    // from the raw yaml.Node, so an UNQUOTED tag keeps its literal text no
    // matter what it looks like numerically. Measured directly against Go
    // (see image_values_test.go in the 2.x tree):
    //   tag: 17.10 -> "postgres:17.10"   tag: 17.9  -> "postgres:17.9"
    //   tag: 18    -> "postgres:18"      tag: latest -> "postgres:latest"
    //   tag: "17.9" (quoted) -> "postgres:17.9"
    // Every case here must equal Go's answer, or the two launchers pin two
    // different images off the same workspace-v1.yml.
    @Test
    fun `an unquoted tag with a trailing zero keeps its exact text - map form`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                repository: postgres
                tag: 17.10
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.10")
    }

    @Test
    fun `an unquoted two-component tag keeps its exact text - map form`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                repository: postgres
                tag: 17.9
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    @Test
    fun `an unquoted single-component numeric tag keeps its exact text - map form`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                repository: postgres
                tag: 18
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:18")
    }

    @Test
    fun `a quoted tag is read unchanged - map form`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                repository: postgres
                tag: "17.9"
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.9")
    }

    @Test
    fun `a non-numeric tag is read unchanged - map form`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                repository: postgres
                tag: latest
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:latest")
    }

    // Same shapes again, as the FIRST element of a list, since the typed
    // blocks take the first rung of a list independently of the map decoding.
    @Test
    fun `an unquoted tag with a trailing zero keeps its exact text - first element of a list`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                - repository: postgres
                  tag: 17.10
                - repository: postgres
                  tag: 18.6
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:17.10")
    }

    @Test
    fun `an unquoted single-component numeric tag keeps its exact text - first element of a list`() {
        val cfg = readConfig(
            """
            postgres:
              image:
                - repository: postgres
                  tag: 18
                - repository: postgres
                  tag: 19
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("postgres:18")
    }

    // Go's decodeImageValues reads a bare scalar `image:` value from the raw
    // node text, so a non-string scalar still names a version: `image: 123`
    // resolves to "postgres:" is wrong shorthand here - the whole `image:`
    // value itself is the scalar, not a repository/tag pair, so the correct
    // reading is the literal text "123" (matching Go's ScalarNode case),
    // exactly as measured against decodeImageValues.
    @Test
    fun `a bare non-string scalar image value keeps its exact text`() {
        val cfg = readConfig(
            """
            postgres:
              image: 123
            """.trimIndent()
        )
        assertThat(cfg.postgres.image).isEqualTo("123")
    }
}
