package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// M6.1 team management: an invited email and whether an account exists yet.
@Serializable
data class TeamMember(
    val email: String,
    val joined: Boolean,
    @SerialName("added_at") val addedAt: String,
)

@Serializable
data class TeamList(
    val members: List<TeamMember> = emptyList(),
)

@Serializable
data class TeamMemberInput(
    val email: String,
)
