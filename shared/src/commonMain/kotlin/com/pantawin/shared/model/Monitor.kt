package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// Full monitor record — mirrors the server's monitorResponse (M1 CRUD).
@Serializable
data class Monitor(
    val id: Long,
    val name: String,
    val url: String,
    val method: String,
    @SerialName("interval_seconds") val intervalSeconds: Int,
    @SerialName("timeout_ms") val timeoutMs: Int,
    @SerialName("expected_status_min") val expectedStatusMin: Int,
    @SerialName("expected_status_max") val expectedStatusMax: Int,
    @SerialName("failure_threshold") val failureThreshold: Int,
    val status: MonitorState,
    // "personal" (owner-only) or "team" (shared with every user, M6).
    // Defaults keep decoding compatible with pre-M6 servers.
    val scope: String = "personal",
    @SerialName("created_at") val createdAt: String,
)

// Request body for create / update. Null fields are omitted so PATCH leaves
// them unchanged server-side.
@Serializable
data class MonitorInput(
    val name: String? = null,
    val url: String? = null,
    val method: String? = null,
    @SerialName("interval_seconds") val intervalSeconds: Int? = null,
    @SerialName("timeout_ms") val timeoutMs: Int? = null,
    @SerialName("failure_threshold") val failureThreshold: Int? = null,
    // Subset of ["email", "push"] (M3 per-monitor channel toggle).
    @SerialName("alert_channels") val alertChannels: List<String>? = null,
    // "personal" or "team" (M6); null = server default (personal).
    val scope: String? = null,
)
