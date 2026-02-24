import com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar
import java.security.MessageDigest

plugins {
    kotlin("jvm")
    id("org.jlleitschuh.gradle.ktlint")
    id("com.gradleup.shadow")
}

group = "ru.citeck.launcher"
version = rootProject.version

val distName = "citeck-cli"

repositories {
    mavenCentral()
    mavenLocal()
}

dependencies {

    implementation(project(":core"))

    val ktorV = project.properties["ktor.version"]

    // Ktor server (daemon)
    implementation("io.ktor:ktor-server-core-jvm:$ktorV")
    implementation("io.ktor:ktor-server-cio:$ktorV")
    implementation("io.ktor:ktor-server-content-negotiation:$ktorV")
    implementation("io.ktor:ktor-server-websockets:$ktorV")
    implementation("io.ktor:ktor-serialization-jackson:$ktorV")

    // Ktor client (CLI commands)
    implementation("io.ktor:ktor-client-core:$ktorV")
    implementation("io.ktor:ktor-client-cio:$ktorV")
    implementation("io.ktor:ktor-client-content-negotiation:$ktorV")
    implementation("io.ktor:ktor-client-websockets:$ktorV")

    implementation("com.github.ajalt.clikt:clikt:5.0.3")
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

tasks.named<ShadowJar>("shadowJar") {
    mergeServiceFiles()
    isReproducibleFileOrder = true
    archiveClassifier = null as String?
    archiveVersion = project.version.toString()
    archiveBaseName = distName
    manifest {
        attributes("Main-Class" to "ru.citeck.launcher.cli.CliMainKt")
    }
}

// --- Linux x64 distribution with embedded JRE (jlink) ---
// To verify modules: jdeps --print-module-deps --ignore-missing-deps citeck-cli.jar

val jlinkModules = listOf(
    "java.compiler",
    "java.instrument",
    "java.logging",
    "java.management",
    "java.naming",
    "java.security.jgss",
    "java.sql",
    "jdk.crypto.ec",
    "jdk.unsupported"
)

val jlinkJre by tasks.registering {
    dependsOn("shadowJar")

    val jreDir = layout.buildDirectory.dir("dist/jre")
    outputs.dir(jreDir)

    doLast {
        val outDir = jreDir.get().asFile
        if (outDir.exists()) outDir.deleteRecursively()

        val javaHome = System.getProperty("java.home")

        val process = ProcessBuilder(
            "$javaHome/bin/jlink",
            "--add-modules",
            jlinkModules.joinToString(","),
            "--strip-debug",
            "--no-man-pages",
            "--no-header-files",
            "--output",
            outDir.absolutePath
        ).inheritIO().start()

        val exitCode = process.waitFor()
        if (exitCode != 0) {
            throw GradleException("jlink failed with exit code $exitCode")
        }
    }
}

val prepareDist by tasks.registering {
    dependsOn("shadowJar", jlinkJre)

    val packageDir = layout.buildDirectory.dir("dist/package")
    outputs.dir(packageDir)

    doLast {
        val outDir = packageDir.get().asFile
        if (outDir.exists()) outDir.deleteRecursively()
        outDir.mkdirs()

        // Copy JRE
        val jreDir = layout.buildDirectory.dir("dist/jre").get().asFile
        jreDir.copyRecursively(File(outDir, "jre"))

        // Copy shadow JAR
        val libDir = File(outDir, "lib")
        libDir.mkdirs()
        val shadowJar = tasks.named<ShadowJar>("shadowJar").get()
        shadowJar.archiveFile.get().asFile.copyTo(File(libDir, "citeck-cli.jar"))

        // Copy launcher script for tar.gz distribution
        file("src/dist/citeck.sh").copyTo(File(outDir, "citeck.sh"))
    }
}

val distTar by tasks.registering(Tar::class) {
    dependsOn(prepareDist)

    val prefix = "$distName-${project.version}-linux_x64"

    archiveBaseName = distName
    archiveVersion = project.version.toString()
    archiveClassifier = "linux_x64"
    compression = Compression.GZIP
    archiveExtension = "tar.gz"

    from(layout.buildDirectory.dir("dist/package")) {
        into(prefix)
        filesMatching("*.sh") {
            permissions { unix("rwxr-xr-x") }
        }
        filesMatching("jre/bin/**") {
            permissions { unix("rwxr-xr-x") }
        }
    }

    destinationDirectory = layout.buildDirectory.dir("dist")
}

val distInstallScript by tasks.registering {
    dependsOn(distTar)

    val outputFile = layout.buildDirectory.file("dist/citeck-install.sh")
    outputs.file(outputFile)

    doLast {
        val tarFile = distTar.get().archiveFile.get().asFile
        val version = project.version.toString()
        val archiveName = tarFile.name

        val sha256 = MessageDigest.getInstance("SHA-256")
            .digest(tarFile.readBytes())
            .joinToString("") { byte -> "%02x".format(byte.toInt() and 0xff) }

        val script = file("src/dist/citeck-install.sh").readText()
            .replace("{{VERSION}}", version)
            .replace("{{SHA256}}", sha256)
            .replace("{{ARCHIVE_NAME}}", archiveName)

        val outFile = outputFile.get().asFile
        outFile.writeText(script)
        outFile.setExecutable(true)
    }
}

tasks.register("dist") {
    group = "distribution"
    description = "Build CLI distribution: tar.gz archive + install script"
    dependsOn(distInstallScript)
}
