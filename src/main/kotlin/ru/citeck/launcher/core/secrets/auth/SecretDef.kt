package ru.citeck.launcher.core.secrets.auth

import ru.citeck.launcher.core.utils.data.DataValue

class SecretDef(
    val id: String,
    val type: AuthType,
    val params: DataValue = DataValue.createObj()
)
