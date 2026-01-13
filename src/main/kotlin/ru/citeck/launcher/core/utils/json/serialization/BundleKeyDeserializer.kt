package ru.citeck.launcher.core.utils.json.serialization

import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.core.JsonToken
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.deser.std.StdDeserializer
import com.fasterxml.jackson.databind.node.ObjectNode
import com.fasterxml.jackson.databind.node.TextNode
import ru.citeck.launcher.core.bundle.BundleKey

class BundleKeyDeserializer : StdDeserializer<BundleKey>(BundleKey::class.java) {

    override fun deserialize(parser: JsonParser, context: DeserializationContext): BundleKey? {
        return when (parser.currentToken) {
            JsonToken.VALUE_STRING -> BundleKey(parser.text)
            JsonToken.START_OBJECT -> {
                val objectData = parser.readValueAsTree<ObjectNode>()
                val rawKey = objectData.get("rawKey")
                if (rawKey is TextNode) {
                    BundleKey(rawKey.asText())
                } else {
                    error("Invalid bundle key value: $objectData")
                }
            }
            else -> context.handleUnexpectedToken(BundleKey::class.java, parser) as? BundleKey
        }
    }

    override fun isCachable(): Boolean {
        return true
    }
}
