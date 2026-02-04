package ru.citeck.launcher.core.utils

enum class CompressionAlg(val extension: String) {

    ZSTD("zst"),
    XZ("xz");

    companion object {
        val MINIMAL_SIZE = XZ
        val OPTIMAL_SIZE_SPEED = ZSTD
    }
}
