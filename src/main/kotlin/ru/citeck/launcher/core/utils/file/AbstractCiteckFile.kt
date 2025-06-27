package ru.citeck.launcher.core.utils.file

abstract class AbstractCiteckFile : CiteckFile {

    override fun readBytes(): ByteArray {
        return read { it.readBytes() }
    }

    override fun copyTo(dest: CiteckFile, filter: (CiteckFile) -> Boolean) {
        if (isFile()) {
            if (filter(this)) {
                dest.write { out -> read { it.copyTo(out) } }
            }
        } else {
            for (child in getChildren()) {
                if (filter(child)) {
                    if (child.isFile()) {
                        dest.createFile(child.getName()) { out ->
                            child.read { it.copyTo(out) }
                        }
                    } else {
                        child.copyTo(dest.getOrCreateDir(child.getName()), filter)
                    }
                }
            }
        }
    }
}
