package ru.citeck.launcher.core.utils.json.serialization

import com.fasterxml.jackson.core.JsonGenerator
import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.core.JsonToken
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.SerializerProvider
import com.fasterxml.jackson.databind.deser.std.StdDeserializer
import com.fasterxml.jackson.databind.ser.std.StdSerializer
import java.time.Duration

class DurationSerializer : StdSerializer<Duration>(Duration::class.java) {

    override fun serialize(value: Duration, gen: JsonGenerator, provider: SerializerProvider) {
        gen.writeString(value.toString())
    }
}

class DurationDeserializer : StdDeserializer<Duration>(Duration::class.java) {

    override fun deserialize(p: JsonParser, ctxt: DeserializationContext): Duration? {

        return when (p.currentToken) {
            JsonToken.VALUE_STRING -> {
                var textToParse = p.text
                if (textToParse.isBlank()) {
                    null
                } else {
                    textToParse = textToParse.replace(" ", "")
                    if (textToParse.startsWith('P', true) ||
                        textToParse.startsWith("-P", true)
                    ) {

                        return Duration.parse(textToParse)
                    }
                    if (textToParse.length < 2) {
                        error("Invalid duration string: '$textToParse'")
                    }
                    var negative = false
                    if (textToParse[0] == '-') {
                        negative = true
                        textToParse = textToParse.substring(1)
                    }
                    if (textToParse.indexOf('T', 0, true) == -1) {
                        val dayIdx = textToParse.indexOf('D', 0, true)
                        if (dayIdx == -1) {
                            textToParse = "T$textToParse"
                        } else if (dayIdx < textToParse.length - 1) {
                            textToParse = textToParse.substring(0, dayIdx) + "DT" +
                                textToParse.substring(dayIdx + 1)
                        }
                    }
                    if (!textToParse.startsWith("P", true)) {
                        textToParse = "P$textToParse"
                    }
                    if (negative) {
                        textToParse = "-$textToParse"
                    }
                    return Duration.parse(textToParse)
                }
            }
            JsonToken.VALUE_NUMBER_INT -> {
                return Duration.ofSeconds(p.longValue)
            }
            else -> {
                ctxt.handleUnexpectedToken(Duration::class.java, p) as? Duration
            }
        }
    }
}
