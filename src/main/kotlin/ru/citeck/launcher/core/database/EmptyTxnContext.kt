package ru.citeck.launcher.core.database

object EmptyTxnContext : TxnContext {

    override fun doBeforeCommit(action: () -> Unit) {
        action.invoke()
    }

    override fun doAfterCommit(action: () -> Unit) {
        action.invoke()
    }

    override fun doAfterRollback(action: () -> Unit) {}
}
