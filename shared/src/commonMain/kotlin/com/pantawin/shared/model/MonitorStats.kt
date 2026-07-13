package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// One chart point: an hour (period=day) or a day (period=week).
// up_pct is null when the monitor had no activity in the bucket (paused /
// not yet created) — "no data", deliberately distinct from 0%.
@Serializable
data class StatsBucket(
    val ts: String,
    val checks: Int,
    val fails: Int,
    @SerialName("up_pct") val upPct: Double? = null,
    @SerialName("avg_ms") val avgMs: Double? = null,
    @SerialName("p95_ms") val p95Ms: Double? = null,
)

// GET /monitors/{id}/stats response (spec section 4, M4).
@Serializable
data class MonitorStats(
    val period: String,
    val from: String,
    val to: String,
    val checks: Int,
    val fails: Int,
    @SerialName("uptime_pct") val uptimePct: Double? = null,
    @SerialName("avg_ms") val avgMs: Double? = null,
    @SerialName("p95_ms") val p95Ms: Double? = null,
    @SerialName("downtime_s") val downtimeS: Int = 0,
    val buckets: List<StatsBucket> = emptyList(),
)
