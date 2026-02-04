package ru.citeck.launcher.core.database

interface TxnContext {

    fun doBeforeCommit(action: () -> Unit)

    fun doAfterCommit(action: () -> Unit)

    fun doAfterRollback(action: () -> Unit)
}
