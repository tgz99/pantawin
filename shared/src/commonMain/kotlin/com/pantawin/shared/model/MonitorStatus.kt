package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
enum class MonitorState {
    UP, DOWN, PAUSED, PENDING
}

// Mirrors server/internal/monitor/model.go StatusView (GET /v1/monitors —
// spec section 4).
@Serializable
data class MonitorStatus(
    val id: Long,
    val name: String,
    val url: String,
    val status: MonitorState,
    // "personal" or "team" (M6) — drives the dashboard's two sections.
    // Default keeps decoding compatible with pre-M6 servers.
    val scope: String = "personal",
    @SerialName("last_checked_at") val lastCheckedAt: String? = null,
    @SerialName("response_time_ms") val responseTimeMs: Int? = null,
)
