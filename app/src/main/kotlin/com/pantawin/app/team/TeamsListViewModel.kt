package com.pantawin.app.team

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.TeamGateway
import com.pantawin.shared.model.Team
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class TeamsListUiState(
    val loading: Boolean = true,
    val teams: List<Team> = emptyList(),
    val creating: Boolean = false,
    val error: String? = null,
)

/** Every team the account belongs to (M6.3 — an account can join several),
 * plus creating new ones. */
class TeamsListViewModel(private val gateway: TeamGateway) : ViewModel() {

    private val _state = MutableStateFlow(TeamsListUiState())
    val state: StateFlow<TeamsListUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            runCatching { gateway.listTeams() }
                .onSuccess { teams -> _state.update { it.copy(loading = false, teams = teams) } }
                .onFailure { e -> _state.update { it.copy(loading = false, error = e.message ?: "Failed to load teams") } }
        }
    }

    fun create(name: String) {
        val trimmed = name.trim()
        if (trimmed.isBlank()) {
            _state.update { it.copy(error = "Enter a team name") }
            return
        }
        viewModelScope.launch {
            _state.update { it.copy(creating = true, error = null) }
            runCatching { gateway.createTeam(trimmed) }
                .onSuccess {
                    _state.update { it.copy(creating = false) }
                    refresh()
                }
                .onFailure { e -> _state.update { it.copy(creating = false, error = e.message ?: "Failed to create team") } }
        }
    }
}
