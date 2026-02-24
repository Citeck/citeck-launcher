plugins {
    kotlin("jvm")
    `java-library`
    id("org.jlleitschuh.gradle.ktlint")
}

group = "ru.citeck.launcher"
version = rootProject.version

repositories {
    mavenCentral()
    mavenLocal()
}

dependencies {

    implementation("com.h2database:h2:2.4.240")

    val drJavaV = project.properties["docker-java.version"]
    api("com.github.docker-java:docker-java-core:$drJavaV")
    api("com.github.docker-java:docker-java-transport-httpclient5:$drJavaV")

    api("org.snakeyaml:snakeyaml-engine:3.0.1")
    api("com.fasterxml.jackson.core:jackson-databind:2.21.0")
    api("com.fasterxml.jackson.module:jackson-module-kotlin:2.21.0")
    api("org.apache.commons:commons-lang3:3.20.0")
    implementation("commons-codec:commons-codec:1.21.0")

    api("org.eclipse.jgit:org.eclipse.jgit:7.5.0.202512021534-r")
    api("ch.qos.logback:logback-classic:1.5.27")
    api("io.github.oshai:kotlin-logging-jvm:7.0.7")

    val ktorV = project.properties["ktor.version"]
    api("io.ktor:ktor-server-core-jvm:$ktorV")
    api("io.ktor:ktor-server-cio:$ktorV")
    api("io.ktor:ktor-client-core:$ktorV")
    api("io.ktor:ktor-client-cio:$ktorV")
    api("io.ktor:ktor-client-cio-jvm:$ktorV")

    implementation("com.dynatrace.hash4j:hash4j:${property("hash4j.version")}")

    testImplementation("org.assertj:assertj-core:3.27.7")
    testImplementation(kotlin("test"))
}

java {
    sourceCompatibility = JavaVersion.VERSION_25
    targetCompatibility = JavaVersion.VERSION_25
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_25
    }
}
