package com.pantawin.app.data

import com.pantawin.shared.model.Team
import com.pantawin.shared.model.TeamMember

/**
 * Team operations (M6.3): any account can create a team and invite others
 * into it; an account can belong to any number of teams. Per-team calls
 * that 403 (caller isn't a member) surface as PantawinApiException(403),
 * which the UI renders as a friendly notice.
 */
interface TeamGateway {
    suspend fun listTeams(): List<Team>
    suspend fun createTeam(name: String): Team
    suspend fun listMembers(teamId: Long): List<TeamMember>
    suspend fun invite(teamId: Long, email: String)
    suspend fun removeInvite(teamId: Long, email: String)
}

class SessionTeamGateway(private val session: SessionManager) : TeamGateway {
    override suspend fun listTeams() = session.authed { token -> session.api.getTeams(token).teams }
    override suspend fun createTeam(name: String) = session.authed { token -> session.api.createTeam(token, name) }
    override suspend fun listMembers(teamId: Long) =
        session.authed { token -> session.api.getTeamMembers(token, teamId).members }
    override suspend fun invite(teamId: Long, email: String) {
        session.authed { token -> session.api.inviteTeamMember(token, teamId, email) }
    }
    override suspend fun removeInvite(teamId: Long, email: String) {
        session.authed { token -> session.api.removeTeamInvite(token, teamId, email) }
    }
}
