package ru.citeck.launcher.core.utils.json.serialization

import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.core.JsonToken
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.deser.std.StdDeserializer
import ru.citeck.launcher.core.entity.EntityRef

class EntityRefDeserializer: StdDeserializer<EntityRef>(EntityRef::class.java) {

    override fun deserialize(parser: JsonParser, context: DeserializationContext): EntityRef {
        return if (parser.currentToken == JsonToken.VALUE_STRING) {
            EntityRef.valueOf(parser.text)
        } else {
            EntityRef.EMPTY
        }
    }

    override fun isCachable(): Boolean {
        return true
    }

    override fun getNullValue(ctxt: DeserializationContext): EntityRef {
        return EntityRef.EMPTY
    }
}
