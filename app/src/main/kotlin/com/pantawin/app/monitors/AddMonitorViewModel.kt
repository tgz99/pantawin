package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.app.data.TeamGateway
import com.pantawin.shared.model.MonitorInput
import com.pantawin.shared.model.Team
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class AddMonitorState(
    val submitting: Boolean = false,
    val error: String? = null,
    val done: Boolean = false,
    // Teams the account belongs to (M6.3), offered as the share target when
    // scope=team. Empty (once loaded) means "create a team first".
    val teams: List<Team> = emptyList(),
    val teamsLoading: Boolean = true,
)

class AddMonitorViewModel(
    private val gateway: MonitorGateway,
    private val teamGateway: TeamGateway,
) : ViewModel() {

    private val _state = MutableStateFlow(AddMonitorState())
    val state: StateFlow<AddMonitorState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            runCatching { teamGateway.listTeams() }
                .onSuccess { teams -> _state.update { it.copy(teams = teams, teamsLoading = false) } }
                // Non-fatal: the team option just shows no teams to pick from.
                .onFailure { _state.update { it.copy(teamsLoading = false) } }
        }
    }

    fun submit(
        name: String,
        url: String,
        intervalSeconds: Int,
        channels: List<String> = listOf("email", "push"),
        scope: String = "personal",
        teamId: Long? = null,
    ) {
        // Client-side pre-check for fast feedback; the server's SSRF guard is
        // the actual authority.
        val trimmedUrl = url.trim()
        if (!trimmedUrl.startsWith("http://") && !trimmedUrl.startsWith("https://")) {
            _state.update { it.copy(error = "URL must start with http:// or https://") }
            return
        }
        if (channels.isEmpty()) {
            _state.update { it.copy(error = "Pick at least one alert channel") }
            return
        }
        if (scope == "team" && teamId == null) {
            _state.update { it.copy(error = "Pick a team to share this monitor with") }
            return
        }
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            runCatching {
                gateway.create(
                    MonitorInput(
                        name = name.trim().ifBlank { null },
                        url = trimmedUrl,
                        intervalSeconds = intervalSeconds,
                        alertChannels = channels,
                        scope = scope,
                        teamId = teamId,
                    ),
                )
            }.onSuccess {
                _state.update { it.copy(submitting = false, done = true) }
            }.onFailure { e ->
                _state.update { it.copy(submitting = false, error = e.message ?: "Failed to add monitor") }
            }
        }
    }
}
