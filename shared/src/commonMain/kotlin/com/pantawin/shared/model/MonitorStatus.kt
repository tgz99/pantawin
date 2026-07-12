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
    @SerialName("last_checked_at") val lastCheckedAt: String? = null,
    @SerialName("response_time_ms") val responseTimeMs: Int? = null,
)
