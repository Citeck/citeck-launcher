package ru.citeck.launcher

import org.apache.commons.codec.binary.Base32
import org.junit.jupiter.api.Test
import ru.citeck.launcher.view.utils.NumUtils

class TestAbc {

    @Test
    fun test() {

        println(Base32.builder().get().encodeToString(NumUtils.toByteArray(12)).lowercase())
        /* Git.cloneRepository()
            .setURI("")
            .setDirectory(repoDir.toFile())
            .setBranchesToClone(listOf("refs/heads/${repoProps.branch}"))
            .setBranch("refs/heads/${repoProps.branch}")
            .setCredentialsProvider(credentialsProvider)
            .setTimeout(500)
            .call()
            .use {
                hashOfLastCommit = it.getLastCommitHash()
            }*/
    }
}
