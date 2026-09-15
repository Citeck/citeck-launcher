package ru.citeck.launcher.core.utils.json.serialization

import org.snakeyaml.engine.v2.nodes.MappingNode
import org.snakeyaml.engine.v2.nodes.Node
import org.snakeyaml.engine.v2.nodes.NodeType
import org.snakeyaml.engine.v2.nodes.ScalarNode
import org.snakeyaml.engine.v2.nodes.SequenceNode

/**
 * Reads an `image:` value straight off a SnakeYAML `Node` — the tree
 * `org.snakeyaml.engine.v2.api.lowlevel.Compose` hands back after the
 * Parse+Compose stages, BEFORE SnakeYAML's `Construct` stage turns a scalar's
 * literal text into a typed Java object (`Double`, `Long`, `Boolean`…).
 *
 * This exists because `Yaml.read` (`Yaml.kt`) goes through `Construct`
 * first — `Load.loadFromString` — and only THEN hands the constructed native
 * object graph to Jackson (`Json.convert`). By that point an unquoted
 * `tag: 17.10` is already the Java `Double` 17.1: 17.10 and 17.1 are the same
 * double, so nothing downstream of that conversion — including
 * `TypedBlockImageDeserializer`, which only ever sees a Jackson `JsonNode`
 * built FROM that already-lossy object — can recover the trailing zero the
 * operator wrote. A `ScalarNode`'s `.value`, by contrast, is always the
 * literal source text, unconditionally, regardless of what it would resolve
 * to — the same guarantee Go's `yaml.Node.Value` makes (see
 * `internal/bundle/resolver.go`'s `decodeImageValues` in the 2.x tree).
 *
 * Mirrors `decodeImageValues` byte for byte (same three shapes: a scalar, a
 * {repository, tag} map, or a sequence of either; a list resolves to ALL its
 * elements in order — callers that want "the first rung" take
 * `.firstOrNull()`; one unreadable rung poisons the whole list, not just that
 * rung), because both launchers read the same workspace-v1.yml / bundle YAML
 * and must resolve the same value out of it.
 */
object RawImageValues {

    fun decodeImageValues(node: Node): List<String> {
        return when (node.nodeType) {
            NodeType.SCALAR -> {
                val v = (node as ScalarNode).value.trim()
                if (v.isEmpty()) emptyList() else listOf(v)
            }
            NodeType.MAPPING -> {
                val repository = scalarChild(node as MappingNode, "repository")
                val tag = scalarChild(node, "tag")
                if (repository.isNullOrEmpty() || tag.isNullOrEmpty()) {
                    emptyList()
                } else {
                    listOf("$repository:$tag")
                }
            }
            NodeType.SEQUENCE -> {
                val items = (node as SequenceNode).value
                val out = ArrayList<String>(items.size)
                for (item in items) {
                    val one = decodeImageValues(item)
                    if (one.size != 1) {
                        // Either unreadable or itself a sequence. Both poison
                        // the whole list: see the type doc comment.
                        return emptyList()
                    }
                    out.add(one[0])
                }
                out
            }
            else -> emptyList()
        }
    }

    /**
     * Finds `key`'s child node directly under a mapping node, mirroring
     * `Node#get(String)` navigation on the equivalent constructed structure —
     * one level only, since every caller here looks up a single well-known
     * key (`image`, `repository`, `tag`) rather than an arbitrary path.
     */
    fun mappingChild(node: Node, key: String): Node? {
        if (node.nodeType != NodeType.MAPPING) {
            return null
        }
        for (tuple in (node as MappingNode).value) {
            val k = tuple.keyNode
            if (k.nodeType == NodeType.SCALAR && (k as ScalarNode).value == key) {
                return tuple.valueNode
            }
        }
        return null
    }

    /**
     * The first element of `node` — itself, or the first item if it is a
     * sequence, null for an empty sequence. Unlike `decodeImageValues`, this
     * does NOT check the rest of a sequence for an unreadable rung: BundleUtils
     * (the only caller) never applied that rule at this level either — it
     * just took `imageNode[0]` — so this keeps that existing behavior rather
     * than tightening it as a side effect of the precision fix.
     */
    fun firstElement(node: Node): Node? {
        return if (node.nodeType == NodeType.SEQUENCE) (node as SequenceNode).value.firstOrNull() else node
    }

    /**
     * The exact source text of `node`'s first element, if it (still) is a
     * scalar — used by callers that, like `TypedBlockImageDeserializer`'s
     * scalar branch, pass the text on for further processing (a registry
     * rewrite) rather than needing it split into a {repository, tag} pair.
     */
    fun firstScalarText(node: Node): String? {
        val first = firstElement(node) ?: return null
        if (first.nodeType != NodeType.SCALAR) {
            return null
        }
        val v = (first as ScalarNode).value.trim()
        return v.ifEmpty { null }
    }

    /**
     * The first element's `repository` and `tag` as their exact source text,
     * when that first element is a {repository, tag} map — used by callers
     * that (unlike `decodeImageValues`) need the two fields SEPARATELY, e.g.
     * to run only the repository through a registry-prefix rewrite.
     */
    fun firstRepositoryAndTag(node: Node): Pair<String, String>? {
        val first = firstElement(node) ?: return null
        if (first.nodeType != NodeType.MAPPING) {
            return null
        }
        val repository = scalarChild(first as MappingNode, "repository")
        val tag = scalarChild(first, "tag")
        return if (repository.isNullOrEmpty() || tag.isNullOrEmpty()) null else repository to tag
    }

    private fun scalarChild(node: MappingNode, key: String): String? {
        val child = mappingChild(node, key) ?: return null
        return if (child.nodeType == NodeType.SCALAR) (child as ScalarNode).value else null
    }
}
