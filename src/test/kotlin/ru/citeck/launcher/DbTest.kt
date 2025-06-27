package ru.citeck.launcher

import org.h2.mvstore.MVStore
import org.h2.mvstore.WriteBuffer
import org.h2.mvstore.tx.TransactionMap
import org.h2.mvstore.tx.TransactionStore
import org.h2.mvstore.type.BasicDataType
import org.h2.mvstore.type.ByteArrayDataType
import org.h2.mvstore.type.StringDataType
import org.junit.jupiter.api.Disabled
import org.junit.jupiter.api.Test
import ru.citeck.launcher.core.utils.json.Json
import java.io.File
import java.nio.ByteBuffer
import java.util.*

class DbTest {

    @Disabled
    @Test
    fun recovery() {

        val mvStore = MVStore.Builder()
            .fileName(File("./test-db.db").absolutePath)
            .recoveryMode()
            .compress()
            .open()


        val txStore = TransactionStore(mvStore)
        println(txStore)

        //txStore.openTransactions.forEach { it.commit() }

        mvStore.close()

    }

    @Disabled
    @Test
    fun test() {

        val mvStore = MVStore.Builder()
            .fileName(File("./test-db.db").absolutePath)
            .compress()
            .open()

        mvStore.openMetaMap().entries.forEach {
            println(it.key + " = " + it.value)
        }

/*        val mapNonTxn: MVMap<String, CustomClass> = mvStore.openMap("customMap",
            MVMap.Builder<String, CustomClass>().valueType(CustomClassDataT2ype())
        )*/

        val txStore = TransactionStore(mvStore)

        val txn = txStore.begin()

        val map: TransactionMap<String, ByteArray> = txn.openMap("customMap2", StringDataType.INSTANCE, ByteArrayDataType.INSTANCE)

/*        val map: MVMap<String, CustomClass> = mvStore.openMap("customMap",
            MVMap.Builder<String, CustomClass>().valueType(CustomClassDataT2ype())
        )*/

        println(map.size)

        map[UUID.randomUUID().toString()] = Json.toBytes(CustomClass("custo22312322m"))

        println(map.size)
        txn.commit()

        map.values.forEach { println(Json.readJson(it)) }

        mvStore.close()

    }

    data class CustomClass(
        val field: String
    ) {
        init {
            println("CREATED!")
        }
    }

    class CustomClassDataT2ype : BasicDataType<CustomClass>() {

        override fun getMemory(obj: CustomClass?): Int {
            if (obj == null) {
                return 0
            }
            return Json.toJson(obj).toString().toByteArray().size
        }

        override fun write(buff: WriteBuffer, obj: CustomClass?) {
            if (obj == null) {
                buff.putInt(0)
            } else {
                val bytes = Json.toJson(obj).toString().toByteArray()
                buff.putInt(bytes.size)
                buff.put(bytes)
            }
        }

        override fun read(buff: ByteBuffer): CustomClass? {
            val size = buff.getInt()
            if (size == 0) {
                return null
            }
            val bytes= ByteArray(size)
            buff.get(bytes)
            return Json.read(bytes, CustomClass::class)
        }

        override fun createStorage(size: Int): Array<CustomClass?> {
            return Array(size) { null }
        }
    }
}
