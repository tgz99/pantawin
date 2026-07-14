package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// M6.3: teams are plural and self-service — any account can create one and
// belong to several.
@Serializable
data class Team(
    val id: Long,
    val name: String,
    @SerialName("owner_id") val ownerId: Long,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class TeamsResponse(
    val teams: List<Team> = emptyList(),
)

@Serializable
data class CreateTeamRequest(
    val name: String,
)

// An invited email and whether an account has joined yet.
@Serializable
data class TeamMember(
    val email: String,
    val joined: Boolean,
    @SerialName("added_at") val addedAt: String,
)

@Serializable
data class TeamMembersResponse(
    val members: List<TeamMember> = emptyList(),
)

@Serializable
data class TeamMemberInput(
    val email: String,
)
