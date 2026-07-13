package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// Mirrors server/internal/realtime.Event — the live WebSocket feed.
@Serializable
data class RealtimeEvent(
    val type: String, // "status" | "incident"
    @SerialName("monitor_id") val monitorId: Long,
    @SerialName("monitor_name") val monitorName: String = "",
    val status: String,
    @SerialName("response_time_ms") val responseTimeMs: Int? = null,
    @SerialName("incident_event") val incidentEvent: String? = null, // "DOWN" | "RECOVERED"
    val at: String,
)
