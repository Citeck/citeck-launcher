package ru.citeck.launcher.core.namespace.runtime

class DockerImageNotFound(image: String) : RuntimeException("Image not found: $image")
