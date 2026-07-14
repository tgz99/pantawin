package com.pantawin.app.data

import com.pantawin.shared.model.TeamMember

/**
 * Team management operations (M6.1). Server enforces admin-only; non-admin
 * calls surface as PantawinApiException(403) which the UI renders as a
 * friendly notice.
 */
interface TeamGateway {
    suspend fun list(): List<TeamMember>
    suspend fun add(email: String)
    suspend fun remove(email: String)
}

class SessionTeamGateway(private val session: SessionManager) : TeamGateway {
    override suspend fun list() = session.authed { token -> session.api.getTeam(token).members }
    override suspend fun add(email: String) { session.authed { token -> session.api.addTeamMember(token, email) } }
    override suspend fun remove(email: String) { session.authed { token -> session.api.removeTeamMember(token, email) } }
}
