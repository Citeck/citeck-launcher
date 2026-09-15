package ru.citeck.launcher.core.utils.json.serialization

import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.JsonNode
import com.fasterxml.jackson.databind.deser.std.StdDeserializer

/**
 * Reads one of the workspace config's TYPED image fields — `postgres.image`,
 * `keycloak.image`, `zookeeper.image`, `onlyoffice.image`, `pgadmin.image`,
 * `sttSidecar.image` — which may be written as a plain "repo:tag" string, a
 * {repository, tag} map, or a LIST of either.
 *
 * These fields are `String`, so without this the YAML sequence the 2.x
 * launcher now accepts here (`internal/bundle/resolver.go`'s `ImageRef`,
 * added alongside the `dependencies:` ladder feature) threw a Jackson
 * `MismatchedInputException` that took down the WHOLE workspace config, not
 * just the one entry — measured directly: `Yaml.read` on a workspace config
 * with `postgres: {image: [postgres:17.9, postgres:18.6]}` throws before
 * `imageRepos`, `webapps` or anything else is read, so the workspace does not
 * open at all.
 *
 * A typed block is read by every launcher, including this one, which has no
 * dependency pin, no hold and no migration — so the only honest reading of a
 * list here is the most conservative rung: the FIRST element, the version
 * most likely to already match what is on the volume. This mirrors
 * `bundle.ImageRef` byte for byte (same three shapes, same "first element of
 * the list", same "unreadable resolves to empty"), because both launchers
 * read the same workspace-v1.yml and must resolve the same value out of it —
 * see AGENTS.md rule (6).
 */
class TypedBlockImageDeserializer : StdDeserializer<String>(String::class.java) {

    override fun deserialize(parser: JsonParser, context: DeserializationContext): String {
        val node = parser.readValueAsTree<JsonNode>()
        return readImageValues(node).firstOrNull() ?: ""
    }

    override fun isCachable(): Boolean {
        return true
    }

    companion object {

        /**
         * Mirrors `decodeImageValues` in `internal/bundle/resolver.go`: a
         * scalar string, a {repository, tag} map, or a sequence of either. A
         * sequence containing anything that isn't cleanly one of the first two
         * shapes (including a nested sequence) is unreadable as a WHOLE and
         * resolves to no values at all, rather than silently dropping the bad
         * element.
         */
        fun readImageValues(node: JsonNode): List<String> {
            return when {
                node.isTextual -> {
                    val v = node.asText().trim()
                    if (v.isEmpty()) emptyList() else listOf(v)
                }
                node.isObject -> {
                    val repository = node.get("repository")?.asText("") ?: ""
                    val tag = node.get("tag")?.asText("") ?: ""
                    if (repository.isEmpty() || tag.isEmpty()) {
                        emptyList()
                    } else {
                        listOf("$repository:$tag")
                    }
                }
                node.isArray -> {
                    val out = ArrayList<String>(node.size())
                    for (item in node) {
                        val one = readImageValues(item)
                        if (one.size != 1) {
                            return emptyList()
                        }
                        out.add(one[0])
                    }
                    out
                }
                else -> emptyList()
            }
        }
    }
}
