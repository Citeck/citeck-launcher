package ru.citeck.launcher.core.database

import io.github.oshai.kotlinlogging.KotlinLogging
import org.h2.mvstore.tx.Transaction
import org.h2.mvstore.tx.TransactionMap
import org.h2.mvstore.type.ByteArrayDataType
import org.h2.mvstore.type.LongDataType
import org.h2.mvstore.type.StringDataType
import ru.citeck.launcher.core.entity.EntityIdType

class TxnContextImpl(
    private val level: Int,
    private val transaction: Transaction,
    private val database: Database
) : TxnContext {
    companion object {
        private val log = KotlinLogging.logger { }
    }

    init {
        log.debug { "Transaction created: ${transaction.id}" }
    }

    private val maps = HashMap<String, TransactionMap<*, ByteArray>>()
    private val afterCommitActions = ArrayList<() -> Unit>()
    private val afterRollbackActions = ArrayList<() -> Unit>()
    private val beforeCommitActions = ArrayList<() -> Unit>()

    override fun doBeforeCommit(action: () -> Unit) {
        beforeCommitActions.add(action)
    }

    override fun doAfterCommit(action: () -> Unit) {
        afterCommitActions.add(action)
    }

    override fun doAfterRollback(action: () -> Unit) {
        afterRollbackActions.add(action)
    }

    internal fun commit() {
        beforeCommitActions.forEach { it.invoke() }
        transaction.commit()
        doActionsAfterTxn(afterCommitActions)
    }

    internal fun rollback() {
        try {
            transaction.rollback()
        } finally {
            doActionsAfterTxn(afterRollbackActions)
        }
    }

    private fun doActionsAfterTxn(actions: List<() -> Unit>) {
        if (actions.isEmpty()) {
            return
        }
        if (level >= 10) {
            log.warn {
                "Transaction level overflow: $level. " +
                    "Actions after transaction won't be executed: \n" + actions.joinToString { it.toString() + "\n" }
            }
            return
        }
        database.doWithinNewTxn(level + 1) {
            actions.forEach {
                try {
                    it.invoke()
                } catch (e: Throwable) {
                    log.error(e) { "[${transaction.id}] Action after transaction failed" }
                }
            }
        }
    }

    fun <K : Any> getMap(repoKey: String, keyType: EntityIdType<K>): TransactionMap<K, ByteArray> {
        @Suppress("UNCHECKED_CAST")
        return maps.computeIfAbsent(repoKey) {
            val keyDataType = when (keyType) {
                EntityIdType.String -> StringDataType.INSTANCE
                EntityIdType.Long -> LongDataType.INSTANCE
            }
            transaction.openMap(repoKey, keyDataType, ByteArrayDataType.INSTANCE)
        } as TransactionMap<K, ByteArray>
    }
}
