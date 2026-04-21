pluginManagement {
    repositories {
        mavenCentral()
        maven("https://redirector.kotlinlang.org/maven/compose-dev")
        google()
        gradlePluginPortal()
    }

    plugins {
        kotlin("jvm").version(extra["kotlin.version"] as String)
        id("org.jetbrains.compose").version(extra["compose.version"] as String)
        id("org.jetbrains.kotlin.plugin.compose").version(extra["kotlin.version"] as String)
        id("org.jlleitschuh.gradle.ktlint").version(extra["ktlint.version"] as String)
        id("com.gradleup.shadow").version(extra["shadow.version"] as String)
    }
}

rootProject.name = "citeck-launcher"
