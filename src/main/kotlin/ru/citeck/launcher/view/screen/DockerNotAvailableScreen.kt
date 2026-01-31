package ru.citeck.launcher.view.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import ru.citeck.launcher.core.namespace.runtime.docker.exception.DockerNotAvailableException
import java.awt.Desktop
import java.net.URI

private const val DOCKER_INSTALL_URL = "https://docs.docker.com/get-docker/"

@Composable
fun DockerNotAvailableScreen(exception: DockerNotAvailableException, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.align(Alignment.Center),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Text(
                text = "Docker is not available",
                fontSize = 2.em
            )
            Spacer(modifier = Modifier.height(16.dp))
            if (exception.isDockerNotRunning) {
                Text(
                    text = "Docker is installed but not running.\nPlease start Docker and click Retry."
                )
            } else {
                Text(
                    text = "Docker does not appear to be installed or is not running."
                )
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = "If Docker is already installed, please start it and click Retry."
                )
                Spacer(modifier = Modifier.height(8.dp))
                val linkText = buildAnnotatedString {
                    append("Install Docker: ")
                    withStyle(
                        SpanStyle(
                            color = MaterialTheme.colorScheme.primary
                        )
                    ) {
                        append(DOCKER_INSTALL_URL)
                    }
                }
                Text(
                    text = linkText,
                    modifier = Modifier.clickable {
                        Desktop.getDesktop().browse(URI(DOCKER_INSTALL_URL))
                    }
                )
            }
            Spacer(modifier = Modifier.height(24.dp))
            Button(onClick = onRetry) {
                Text("Retry")
            }
        }
    }
}
