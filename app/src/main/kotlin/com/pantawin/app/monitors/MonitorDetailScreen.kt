package com.pantawin.app.monitors

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.pantawin.app.ui.ErrorState
import com.pantawin.app.ui.LoadingState
import com.pantawin.app.ui.theme.chartColors
import com.pantawin.app.ui.theme.uptimeColor
import com.pantawin.app.ui.visual
import com.pantawin.shared.model.MonitorStats
import com.pantawin.shared.model.StatsBucket
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlin.math.roundToInt

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MonitorDetailScreen(
    viewModel: MonitorDetailViewModel,
    onBack: () -> Unit,
) {
    val state by viewModel.state.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        state.monitor?.name ?: "Monitor",
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        when {
            state.loading && state.stats == null -> LoadingState(Modifier.padding(padding))
            state.error != null && state.stats == null ->
                ErrorState(state.error ?: "", onRetry = viewModel::load, modifier = Modifier.padding(padding))
            else -> {
                val stats = state.stats ?: return@Scaffold
                Column(
                    Modifier
                        .padding(padding)
                        .fillMaxSize()
                        .verticalScroll(rememberScrollState())
                        .padding(horizontal = 16.dp),
                ) {
                    state.monitor?.let { m ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            val visual = m.status.visual()
                            Icon(visual.icon, contentDescription = visual.label, tint = visual.color, modifier = Modifier.size(18.dp))
                            Text(
                                "${visual.label} · ${m.url.removePrefix("https://").removePrefix("http://")}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.padding(start = 6.dp),
                            )
                        }
                    }

                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        modifier = Modifier.padding(top = 16.dp),
                    ) {
                        PeriodChip("Day", selected = state.period == "day") { viewModel.setPeriod("day") }
                        PeriodChip("Week", selected = state.period == "week") { viewModel.setPeriod("week") }
                    }

                    ElevatedCard(
                        shape = MaterialTheme.shapes.large,
                        modifier = Modifier.fillMaxWidth().padding(top = 16.dp),
                    ) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier.padding(20.dp),
                        ) {
                            UptimeRing(uptimePct = stats.uptimePct, modifier = Modifier.size(120.dp))
                            Column(Modifier.padding(start = 20.dp)) {
                                StatLine("Avg response", stats.avgMs?.let { "${it.roundToInt()} ms" } ?: "–")
                                StatLine("P95 response", stats.p95Ms?.let { "${it.roundToInt()} ms" } ?: "–")
                                StatLine("Checks", "${stats.checks} (${stats.fails} failed)")
                                StatLine("Downtime", humanDowntime(stats.downtimeS))
                            }
                        }
                    }

                    ElevatedCard(
                        shape = MaterialTheme.shapes.large,
                        modifier = Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 24.dp),
                    ) {
                        Column(Modifier.padding(20.dp)) {
                            Text(
                                "Response time",
                                style = MaterialTheme.typography.titleSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            ResponseTimeChart(
                                stats = stats,
                                modifier = Modifier.fillMaxWidth().height(180.dp).padding(top = 12.dp),
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun PeriodChip(label: String, selected: Boolean, onClick: () -> Unit) {
    FilterChip(selected = selected, onClick = onClick, label = { Text(label) })
}

@Composable
private fun StatLine(label: String, value: String) {
    Row(modifier = Modifier.padding(vertical = 3.dp)) {
        Text(
            label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Text(value, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.Medium)
    }
}

// Uptime ring: a status-colored arc with the value as text in the center —
// the number carries the information, the color reinforces it.
@Composable
private fun UptimeRing(uptimePct: Double?, modifier: Modifier = Modifier) {
    val palette = chartColors()
    val track = MaterialTheme.colorScheme.surfaceVariant
    val ringColor = uptimePct?.let { palette.uptimeColor(it) } ?: track

    Box(modifier, contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val stroke = Stroke(width = 12.dp.toPx(), cap = StrokeCap.Round)
            val inset = stroke.width / 2
            val arcSize = Size(size.width - stroke.width, size.height - stroke.width)
            drawArc(
                color = track,
                startAngle = -90f, sweepAngle = 360f, useCenter = false,
                topLeft = Offset(inset, inset), size = arcSize, style = stroke,
            )
            uptimePct?.let {
                drawArc(
                    color = ringColor,
                    startAngle = -90f,
                    sweepAngle = (360f * (it / 100.0)).toFloat(),
                    useCenter = false,
                    topLeft = Offset(inset, inset), size = arcSize, style = stroke,
                )
            }
        }
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                uptimePct?.let { formatPct(it) } ?: "–",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
            Text(
                "uptime",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

private fun formatPct(pct: Double): String = when {
    pct >= 100.0 -> "100%"
    // Show enough precision that 99.98% doesn't render as 100%.
    else -> String.format("%.2f%%", pct)
}

private fun humanDowntime(seconds: Int): String = when {
    seconds == 0 -> "none"
    seconds < 60 -> "${seconds}s"
    seconds < 3600 -> "${seconds / 60}m ${seconds % 60}s"
    else -> "${seconds / 3600}h ${(seconds % 3600) / 60}m"
}

// Single-series line chart: 2dp line, soft area fill, three recessive
// gridlines with value labels in text tokens, first/last time labels.
// Buckets without data (paused hours) simply break the line.
@Composable
private fun ResponseTimeChart(stats: MonitorStats, modifier: Modifier = Modifier) {
    val palette = chartColors()
    val gridColor = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.15f)
    val labelColor = MaterialTheme.colorScheme.onSurfaceVariant
    val labelStyle = MaterialTheme.typography.labelSmall.copy(color = labelColor)
    val textMeasurer = rememberTextMeasurer()

    val points = stats.buckets.map { it.avgMs }
    val maxValue = points.filterNotNull().maxOrNull()
    if (maxValue == null || stats.buckets.size < 2) {
        Box(modifier, contentAlignment = Alignment.Center) {
            Text(
                "Not enough data yet",
                style = MaterialTheme.typography.bodySmall,
                color = labelColor,
            )
        }
        return
    }
    val yTop = maxValue * 1.15

    Canvas(modifier) {
        val labelGutter = 42.dp.toPx()
        val bottomGutter = 18.dp.toPx()
        val plotW = size.width - labelGutter
        val plotH = size.height - bottomGutter
        val n = stats.buckets.size
        fun x(i: Int) = labelGutter + plotW * i / (n - 1).toFloat()
        fun y(v: Double) = (plotH * (1 - v / yTop)).toFloat()

        // Recessive grid: three lines with value labels in the gutter.
        for (frac in listOf(0.0, 0.5, 1.0)) {
            val value = yTop * frac
            val yy = y(value)
            drawLine(gridColor, Offset(labelGutter, yy), Offset(size.width, yy), strokeWidth = 1.dp.toPx())
            val layout = textMeasurer.measure("${value.roundToInt()}", labelStyle)
            drawText(
                layout,
                topLeft = Offset(labelGutter - layout.size.width - 6.dp.toPx(), yy - layout.size.height / 2),
            )
        }

        // Line path, broken across null buckets (no data ≠ zero).
        val line = Path()
        val fill = Path()
        var runStart: Int? = null
        fun closeRun(endIdx: Int) {
            val s = runStart ?: return
            if (endIdx > s) {
                fill.lineTo(x(endIdx), plotH)
                fill.lineTo(x(s), plotH)
                fill.close()
            }
            runStart = null
        }
        stats.buckets.forEachIndexed { i, b ->
            val v = b.avgMs
            if (v == null) {
                closeRun(i - 1)
                return@forEachIndexed
            }
            if (runStart == null) {
                runStart = i
                line.moveTo(x(i), y(v))
                fill.moveTo(x(i), y(v))
            } else {
                line.lineTo(x(i), y(v))
                fill.lineTo(x(i), y(v))
            }
        }
        closeRun(n - 1)

        drawPath(
            fill,
            brush = Brush.verticalGradient(
                colors = listOf(palette.series.copy(alpha = 0.18f), Color.Transparent),
                startY = 0f, endY = plotH,
            ),
        )
        drawPath(line, color = palette.series, style = Stroke(width = 2.dp.toPx(), cap = StrokeCap.Round))

        // Latest point marker.
        val lastIdx = stats.buckets.indexOfLast { it.avgMs != null }
        if (lastIdx >= 0) {
            val v = stats.buckets[lastIdx].avgMs!!
            drawCircle(palette.series, radius = 4.dp.toPx(), center = Offset(x(lastIdx), y(v)))
        }

        // First/last time labels along the baseline.
        val zone = ZoneId.systemDefault()
        val fmt = if (stats.period == "day") {
            DateTimeFormatter.ofPattern("HH:mm").withZone(zone)
        } else {
            DateTimeFormatter.ofPattern("d MMM").withZone(ZoneId.of("UTC"))
        }
        val firstLabel = textMeasurer.measure(fmt.format(Instant.parse(stats.buckets.first().ts)), labelStyle)
        drawText(firstLabel, topLeft = Offset(labelGutter, plotH + 4.dp.toPx()))
        val lastLabel = textMeasurer.measure(fmt.format(Instant.parse(stats.buckets.last().ts)), labelStyle)
        drawText(lastLabel, topLeft = Offset(size.width - lastLabel.size.width, plotH + 4.dp.toPx()))
    }
}
