package com.pantawin.app.data

import com.pantawin.shared.model.Monitor
import com.pantawin.shared.model.MonitorInput
import com.pantawin.shared.model.MonitorStats
import com.pantawin.shared.model.MonitorStatus

/**
 * The monitor operations the UI needs, decoupled from token handling and
 * Android Context so ViewModels are unit-testable with a fake. The
 * production impl ([SessionMonitorGateway]) routes every call through
 * [SessionManager.authed] for transparent token refresh.
 */
interface MonitorGateway {
    suspend fun list(): List<MonitorStatus>
    suspend fun create(input: MonitorInput): Monitor
    suspend fun pause(id: Long)
    suspend fun resume(id: Long)
    suspend fun delete(id: Long)
    suspend fun stats(id: Long, period: String): MonitorStats
}

class SessionMonitorGateway(private val session: SessionManager) : MonitorGateway {
    override suspend fun list() = session.authed { token -> session.api.getMonitors(token) }
    override suspend fun create(input: MonitorInput) = session.authed { token -> session.api.createMonitor(token, input) }
    override suspend fun pause(id: Long) { session.authed { token -> session.api.pauseMonitor(token, id) } }
    override suspend fun resume(id: Long) { session.authed { token -> session.api.resumeMonitor(token, id) } }
    override suspend fun delete(id: Long) { session.authed { token -> session.api.deleteMonitor(token, id) } }
    override suspend fun stats(id: Long, period: String) = session.authed { token -> session.api.getStats(token, id, period) }
}
