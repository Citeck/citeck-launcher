package ru.citeck.launcher.view.screen

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.em

@Composable
fun LoadingScreen() {
    Box(modifier = Modifier.fillMaxSize()) {
        Text(
            text = "Loading...",
            fontSize = 2.em,
            modifier = Modifier.align(Alignment.Center),
        )
    }
}
