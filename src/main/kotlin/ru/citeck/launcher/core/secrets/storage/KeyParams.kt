package ru.citeck.launcher.core.secrets.storage

class KeyParams(
    val alg: Int = 0,
    val salt: ByteArray,
    val keySize: Int,
    val iterations: Int
)
