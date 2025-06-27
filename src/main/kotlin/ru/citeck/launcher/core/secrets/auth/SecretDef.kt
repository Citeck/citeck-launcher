package ru.citeck.launcher.core.secrets.auth

import ru.citeck.launcher.core.utils.data.DataValue

class SecretDef(
    val id: String,
    val type: AuthType,
    val params: DataValue = DataValue.createObj()
)
/*
    @JsonTypeInfo(
        use = JsonTypeInfo.Id.NAME,
        include = JsonTypeInfo.As.PROPERTY,
        property = "type"
    )
    @JsonSubTypes(
        JsonSubTypes.Type(value = Basic::class, name = Basic.TYPE)
    )
    sealed class SecretParams

    data object Basic : SecretParams() {
        const val TYPE = "BASIC"
    }*/
