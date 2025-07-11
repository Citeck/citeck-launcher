package ru.citeck.launcher.core.appdef

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import ru.citeck.launcher.core.utils.Digest

@JsonDeserialize(builder = InitContainerDef.Builder::class)
data class InitContainerDef(
    val image: String,
    val environments: Map<String, String>,
    val volumes: List<String>,
    val kind: ApplicationKind,
    val cmd: List<String>?
) {

    companion object {
        val EMPTY = create().build()

        fun create(): Builder {
            return Builder()
        }
    }

    fun copy(): Builder {
        return Builder(this)
    }

    private val hashField: String by lazy {
        Digest.sha256()
            .update(image)
            .update(environments)
            .update(volumes)
            .toHex()
    }



    @JsonIgnore
    fun getHash(): String {
        return hashField
    }

    class Builder() {

        private var image: String = ""
        private var environments: MutableMap<String, String> = LinkedHashMap()
        private var volumes: MutableList<String> = ArrayList()
        private var kind: ApplicationKind = ApplicationKind.THIRD_PARTY
        private var cmd: List<String>? = null

        constructor(base: InitContainerDef) : this() {
            this.image = base.image
            this.environments = LinkedHashMap(base.environments)
            this.volumes = ArrayList(base.volumes)
            this.kind = base.kind
            this.cmd = base.cmd
        }

        fun getEnv(name: String): String? {
            return environments[name]
        }

        fun withImage(image: String): Builder {
            this.image = image
            return this
        }

        fun withCmd(cmd: List<String>?): Builder {
            this.cmd = cmd
            return this
        }

        fun addEnv(key: String, value: String): Builder {
            this.environments[key] = value
            return this
        }

        fun withEnvironments(environments: Map<String, String>): Builder {
            this.environments = LinkedHashMap(environments)
            return this
        }

        fun withVolumes(volumes: List<String>): Builder {
            this.volumes = ArrayList(volumes)
            return this
        }

        fun addVolume(volume: String): Builder {
            this.volumes.add(volume)
            return this
        }

        fun withKind(kind: ApplicationKind?): Builder {
            this.kind = kind ?: ApplicationKind.THIRD_PARTY
            return this
        }

        fun build(): InitContainerDef {
            return InitContainerDef(
                image = image,
                volumes = volumes,
                environments = environments,
                kind = kind,
                cmd = cmd
            )
        }
    }
}
