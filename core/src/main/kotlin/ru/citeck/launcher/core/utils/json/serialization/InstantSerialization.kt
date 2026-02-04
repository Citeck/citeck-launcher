package ru.citeck.launcher.core.utils.json.serialization

import com.fasterxml.jackson.core.JsonGenerator
import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.core.JsonToken
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.SerializerProvider
import com.fasterxml.jackson.databind.deser.std.StdDeserializer
import com.fasterxml.jackson.databind.ser.std.StdSerializer
import java.time.Instant
import java.time.OffsetDateTime

class InstantSerializer : StdSerializer<Instant>(Instant::class.java) {

    override fun serialize(value: Instant, gen: JsonGenerator, prov: SerializerProvider) {
        gen.writeString(value.toString())
    }
}

class InstantDeserializer : StdDeserializer<Instant>(Instant::class.java) {

    override fun deserialize(parser: JsonParser, ctxt: DeserializationContext): Instant? {

        return when (parser.currentToken) {
            JsonToken.VALUE_STRING -> {
                val text = parser.text
                if (text.isEmpty()) {
                    null
                } else {
                    var valueToParse = parser.text
                    if (valueToParse.length == "0000-00-00".length) {
                        valueToParse += "T00:00:00Z"
                    }
                    OffsetDateTime.parse(valueToParse).toInstant()
                }
            }
            else -> {
                ctxt.handleUnexpectedToken(Instant::class.java, parser) as? Instant
            }
        }
    }
}
