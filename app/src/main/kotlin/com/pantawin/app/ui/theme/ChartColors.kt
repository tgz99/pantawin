package com.pantawin.app.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/**
 * Chart palette, validated per mode with the dataviz six-checks validator
 * (lightness band, chroma floor, CVD separation, contrast vs surface) — the
 * dark set is its own selection of deeper steps, not an automatic flip.
 * Status colors are always accompanied by a text label, never color alone.
 */
data class ChartColors(
    val series: Color, // single-series line (response time)
    val good: Color, // uptime ring >= 99.5%
    val degraded: Color, // uptime ring 95–99.5%
    val bad: Color, // uptime ring < 95%
)

private val LightChartColors = ChartColors(
    series = Color(0xFF1565C0),
    good = Color(0xFF2E7D32),
    degraded = Color(0xFFEF6C00),
    bad = Color(0xFFC62828),
)

private val DarkChartColors = ChartColors(
    series = Color(0xFF2196F3),
    good = Color(0xFF43A047),
    degraded = Color(0xFFE65100),
    bad = Color(0xFFEF5350),
)

@Composable
fun chartColors(): ChartColors =
    if (isSystemInDarkTheme()) DarkChartColors else LightChartColors

fun ChartColors.uptimeColor(pct: Double): Color = when {
    pct >= 99.5 -> good
    pct >= 95.0 -> degraded
    else -> bad
}
