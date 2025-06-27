package ru.citeck.launcher.core.database

import ru.citeck.launcher.core.utils.data.DataValue

interface DataRepo : Repository<String, DataValue> {

    operator fun set(id: String, value: Any)

    override operator fun get(id: String): DataValue
}
