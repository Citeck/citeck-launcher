package ru.citeck.launcher.core.database

import com.fasterxml.jackson.databind.JavaType
import com.google.common.io.Files
import io.github.oshai.kotlinlogging.KotlinLogging
import org.h2.mvstore.MVStore
import org.h2.mvstore.tx.TransactionStore
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.exists

class Database {

    companion object {
        private const val SCOPE_DELIM = "!"
        private const val SCOPE_REPLACEMENT = "!!"

        private val log = KotlinLogging.logger {}

        private fun getRepoKey(scope: String, key: String): String {
            return scope.replace(SCOPE_DELIM, SCOPE_REPLACEMENT) + SCOPE_DELIM + key
        }
    }

    private lateinit var store: MVStore
    private lateinit var txStore: TransactionStore

    val transactionContext = ThreadLocal<TxnContextImpl>()

    private val repositories = ConcurrentHashMap<String, Repository<*, *>>()

    fun init() {
        // todo: remove
        val legacyPath = AppDir.PATH.resolve("storage3.db")
        if (legacyPath.exists()) {
            Files.move(legacyPath.toFile(), AppDir.PATH.resolve("storage.db").toFile())
        }
        // ============
        store = MVStore.Builder()
            .fileName(AppDir.PATH.resolve("storage.db").toFile().absolutePath)
            .compress()
            .open()
        txStore = TransactionStore(store)
        txStore.init()

        val openTransactions = txStore.openTransactions
        if (openTransactions.isNotEmpty()) {
            log.warn { "Found ${openTransactions.size} open transactions. Seems that previous app instance closed unexpectedly." }
            openTransactions.forEach {
                try {
                    it.rollback()
                } catch (e: Exception) {
                    log.error(e) { "Error during rollback." }
                }
            }
        }
        Runtime.getRuntime().addShutdownHook(
            Thread {
                if (!store.isClosed) {
                    txStore.close()
                    store.close()
                }
            }
        )
    }

    fun <K : Any, T : Any> getRepo(
        keyType: EntityIdType<K>,
        valueType: JavaType,
        scope: String,
        key: String
    ): Repository<K, T> {
        val repoKey = getRepoKey(scope, key)
        val repo = repositories.computeIfAbsent(repoKey) {
            RepoImpl<K, T>(it, keyType, valueType)
        }
        @Suppress("UNCHECKED_CAST")
        return repo as Repository<K, T>
    }

    fun deleteRepo(scope: String, key: String) {
        store.removeMap(getRepoKey(scope, key))
    }

    fun getDataRepo(
        scope: String,
        key: String
    ): DataRepo {
        val repo = getRepo<String, DataValue>(EntityIdType.String, Json.getSimpleType(DataValue::class), scope, key)
        return object : DataRepo, Repository<String, DataValue> by repo {
            override fun set(id: String, value: Any) {
                repo[id] = DataValue.of(value)
            }
            override fun get(id: String): DataValue {
                return repo[id] ?: DataValue.NULL
            }
        }
    }

    fun getTxnContext(): TxnContext {
        return transactionContext.get() ?: EmptyTxnContext
    }

    fun <T> doWithinNewTxn(action: (TxnContextImpl) -> T): T {
        return doWithinNewTxn(0, action)
    }

    fun <T> doWithinNewTxn(level: Int, action: (TxnContextImpl) -> T): T {
        val txnContextBefore = transactionContext.get()
        val newTxn = txStore.begin()
        val newTxnCtx = TxnContextImpl(level, newTxn, this)
        transactionContext.set(newTxnCtx)
        try {
            val result = action.invoke(newTxnCtx)
            newTxnCtx.commit()
            return result
        } catch (e: Throwable) {
            try {
                newTxnCtx.rollback()
            } catch (rbEx: Throwable) {
                e.addSuppressed(rbEx)
            }
            throw e
        } finally {
            if (txnContextBefore != null) {
                transactionContext.set(txnContextBefore)
            } else {
                transactionContext.remove()
            }
        }
    }

    fun <T> doWithinTxn(action: (TxnContextImpl) -> T): T {
        val txnCtx = transactionContext.get()
        if (txnCtx != null) {
            return action(txnCtx)
        }
        return doWithinNewTxn(action)
    }

    private fun <K : Any, R> doWithRepoMap(
        repoKey: String,
        keyType: EntityIdType<K>,
        action: (MutableMap<K, ByteArray>) -> R
    ): R {
        try {
            val txn = transactionContext.get()
            if (txn != null) {
                return action.invoke(txn.getMap(repoKey, keyType))
            }
            return doWithinTxn { txnCtx ->
                action.invoke(txnCtx.getMap(repoKey, keyType))
            }
        } catch (e: Throwable) {
            log.error(e) { "Action with repo failed. Repo Key: $repoKey. KeyType: $keyType" }
            throw e
        }
    }

    private inner class RepoImpl<K : Any, T : Any>(
        private val repoKey: String,
        private val keyType: EntityIdType<K>,
        private val valueType: JavaType
    ) : Repository<K, T> {

        override fun set(id: K, value: T) {
            doWithRepoMap(repoKey, keyType) { it[id] = Json.toBytes(value) }
        }

        override fun get(id: K): T? {
            return doWithRepoMap(repoKey, keyType) { map -> map[id]?.let { Json.read(it, valueType) } }
        }

        override fun delete(id: K) {
            return doWithRepoMap(repoKey, keyType) { it.remove(id) }
        }

        override fun find(max: Int): List<T> {
            return doWithRepoMap(repoKey, keyType) { map ->
                map.values
                    .asSequence()
                    .take(max)
                    .map { Json.read<T>(it, valueType) }
                    .toList()
            }
        }

        override fun getFirst(): T? {
            return doWithRepoMap(repoKey, keyType) { map ->
                val it = map.iterator()
                if (it.hasNext()) {
                    Json.read<T>(it.next().value, valueType)
                } else {
                    null
                }
            }
        }

        override fun forEach(action: (K, T) -> Boolean) {
            doWithRepoMap(repoKey, keyType) { map ->
                for ((k, v) in map) {
                    if (action.invoke(k, Json.read(v, valueType))) {
                        break
                    }
                }
            }
        }
    }
}
