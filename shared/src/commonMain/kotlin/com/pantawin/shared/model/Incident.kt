package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// One downtime incident (spec section 5, M5 incident history).
// resolvedAt/durationS are null while the incident is ongoing.
@Serializable
data class Incident(
    val id: Long,
    @SerialName("monitor_id") val monitorId: Long,
    @SerialName("started_at") val startedAt: String,
    @SerialName("resolved_at") val resolvedAt: String? = null,
    val cause: String,
    @SerialName("duration_s") val durationS: Long? = null,
) {
    val ongoing: Boolean get() = resolvedAt == null
}

@Serializable
data class IncidentList(
    val incidents: List<Incident> = emptyList(),
)
