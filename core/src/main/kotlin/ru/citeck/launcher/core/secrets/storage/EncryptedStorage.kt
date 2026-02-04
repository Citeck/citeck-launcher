package ru.citeck.launcher.core.secrets.storage

class EncryptedStorage(
    val key: KeyParams,
    val alg: Int,
    val iv: ByteArray,
    val tagLen: Int,
    val data: ByteArray
)
