package ru.citeck.launcher.view.screen

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.ContainerStats
import ru.citeck.launcher.core.namespace.runtime.NamespaceStats
import ru.citeck.launcher.view.commons.CiteckTooltipArea

private val RED_COLOR = Color(0xFFE53935)
private val ORANGE_COLOR = Color(0xFFFFA726)
private val GREEN_COLOR = Color(0xFF66BB6A)

private const val PROGRESS_BAR_ANIMATION_MS = 300

/**
 * Displays CPU and Memory stats cells for a single application container.
 */
@Composable
fun AppStatsCells(
    appStatus: AppRuntimeStatus,
    containerStats: ContainerStats,
    modifier: Modifier = Modifier
) {
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically
    ) {
        val cpuTooltip = if (appStatus == AppRuntimeStatus.RUNNING && containerStats.isCpuThrottled) {
            buildString {
                append("Throttled: ${containerStats.cpuThrottledPeriods} periods")
                append("\nThrottle time: ${ContainerStats.formatNanosToMs(containerStats.cpuThrottledTime)}")
            }
        } else {
            ""
        }
        CiteckTooltipArea(
            tooltip = cpuTooltip,
            modifier = Modifier.width(AppTableColumns.CPU_WIDTH)
        ) {
            StatsCell(
                value = containerStats.cpuPercent,
                text = ContainerStats.formatCpuPercent(containerStats.cpuPercent),
                isActive = appStatus == AppRuntimeStatus.RUNNING,
                isWarning = containerStats.isCpuThrottled
            )
        }

        val memTooltip = if (appStatus == AppRuntimeStatus.RUNNING && containerStats.memoryUsage > 0) {
            buildString {
                if (containerStats.memoryLimit > 0) {
                    append(ContainerStats.formatMemoryFull(containerStats.memoryUsage, containerStats.memoryLimit))
                    append(" (${String.format("%.1f", containerStats.memoryPercent)}%)")
                } else {
                    append(ContainerStats.formatMemoryShort(containerStats.memoryUsage))
                    append(" (no limit)")
                }
                if (containerStats.isMemoryCritical) {
                    append("\nCRITICAL: Near OOM limit!")
                } else if (containerStats.isMemoryWarning) {
                    append("\nWarning: High memory usage")
                }
                if (containerStats.memoryCache > 0) {
                    append("\nCache: ${ContainerStats.formatBytes(containerStats.memoryCache)}")
                }
            }
        } else {
            ""
        }
        CiteckTooltipArea(
            tooltip = memTooltip,
            modifier = Modifier.width(AppTableColumns.MEM_WIDTH)
        ) {
            val hasMemoryData = containerStats.memoryUsage > 0 || containerStats.memoryLimit > 0
            StatsCell(
                value = containerStats.memoryPercent,
                text = ContainerStats.formatMemoryShort(containerStats.memoryUsage),
                isActive = appStatus == AppRuntimeStatus.RUNNING && hasMemoryData,
                isWarning = containerStats.isMemoryWarning,
                isCritical = containerStats.isMemoryCritical
            )
        }
    }
}

@Composable
fun StatsCell(
    value: Double,
    text: String,
    isActive: Boolean,
    modifier: Modifier = Modifier,
    isWarning: Boolean = false,
    isCritical: Boolean = false
) {
    if (!isActive) {
        Text("-", modifier = modifier, color = Color.Gray)
        return
    }

    val textColor = when {
        isCritical -> RED_COLOR
        isWarning -> ORANGE_COLOR
        else -> Color.Unspecified
    }

    val animatedProgress by animateFloatAsState(
        targetValue = (value / 100.0).coerceIn(0.0, 1.0).toFloat(),
        animationSpec = tween(durationMillis = PROGRESS_BAR_ANIMATION_MS)
    )

    val barColor = when {
        isCritical -> RED_COLOR
        isWarning -> ORANGE_COLOR
        else -> GREEN_COLOR
    }

    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        // Text value
        Text(text, maxLines = 1, modifier = Modifier.width(55.dp), color = textColor)

        // Progress bar
        Box(
            modifier = Modifier
                .width(30.dp)
                .height(6.dp)
                .clip(RoundedCornerShape(3.dp))
                .background(Color.Gray.copy(alpha = 0.3f))
        ) {
            Box(
                modifier = Modifier
                    .fillMaxHeight()
                    .fillMaxWidth(fraction = animatedProgress)
                    .background(barColor)
            )
        }
    }
}

@Composable
fun NamespaceStatsSummary(stats: NamespaceStats) {
    val systemInfo = stats.systemInfo

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 10.dp, vertical = 4.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp)
    ) {
        val cpuTooltip = if (systemInfo.cpuCores > 0) {
            "${systemInfo.cpuCores} CPUs available"
        } else {
            ""
        }
        CiteckTooltipArea(tooltip = cpuTooltip) {
            CompactResourceRow(
                label = "CPU",
                currentValue = ContainerStats.formatCpuPercent(stats.totalCpuPercent),
                maxValue = if (systemInfo.cpuCores > 0) {
                    ContainerStats.formatCpuPercent(systemInfo.maxCpuPercent)
                } else {
                    null
                },
                progress = if (systemInfo.maxCpuPercent > 0) {
                    (stats.totalCpuPercent / systemInfo.maxCpuPercent).coerceIn(0.0, 1.0).toFloat()
                } else {
                    0f
                }
            )
        }

        // Memory row
        CompactResourceRow(
            label = "MEM",
            currentValue = ContainerStats.formatBytes(stats.totalMemoryUsage),
            maxValue = if (systemInfo.memoryTotal > 0) {
                ContainerStats.formatBytes(systemInfo.memoryTotal)
            } else {
                null
            },
            progress = if (systemInfo.memoryTotal > 0) {
                (stats.totalMemoryUsage.toDouble() / systemInfo.memoryTotal).coerceIn(0.0, 1.0).toFloat()
            } else {
                0f
            }
        )
    }
}

@Composable
private fun CompactResourceRow(
    label: String,
    currentValue: String,
    maxValue: String?,
    progress: Float,
    modifier: Modifier = Modifier
) {
    val progressPercent = progress * 100
    val barColor = when {
        progressPercent >= 90 -> RED_COLOR
        progressPercent >= 70 -> ORANGE_COLOR
        else -> GREEN_COLOR
    }

    val animatedProgress by animateFloatAsState(
        targetValue = progress,
        animationSpec = tween(durationMillis = PROGRESS_BAR_ANIMATION_MS)
    )

    Row(
        modifier = modifier.fillMaxWidth().height(20.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        // Label
        Text(
            text = label,
            color = Color.Gray,
            fontSize = 0.85.em,
            lineHeight = 0.85.em,
            modifier = Modifier.width(35.dp)
        )

        // Values
        Text(
            text = if (maxValue != null) "$currentValue / $maxValue" else currentValue,
            fontWeight = FontWeight.Medium,
            fontSize = 0.85.em,
            lineHeight = 0.85.em,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f).padding(horizontal = 4.dp)
        )

        // Progress bar
        Box(
            modifier = Modifier
                .width(80.dp)
                .height(6.dp)
                .clip(RoundedCornerShape(3.dp))
                .background(Color.Gray.copy(alpha = 0.2f))
        ) {
            Box(
                modifier = Modifier
                    .fillMaxHeight()
                    .fillMaxWidth(fraction = animatedProgress)
                    .background(barColor)
            )
        }
    }
}
