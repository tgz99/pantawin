package com.pantawin.app.dashboard

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.BuildConfig
import com.pantawin.shared.api.PantawinApiClient
import com.pantawin.shared.api.createPantawinHttpClient
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Hand-rolled ViewModel (no Hilt yet — that lands in M1 as a refactor to
 * constructor injection, not a rewrite; see the M0-M2 plan). [api] is still
 * constructor-injectable (with a production default) so this stays unit
 * testable with a fake/mock client without needing Hilt.
 *
 * Logs in with the bootstrap admin credentials on every load since there's
 * no login screen or persisted session yet (M1).
 */
class DashboardViewModel(
    private val api: PantawinApiClient = PantawinApiClient(createPantawinHttpClient(), BuildConfig.API_BASE_URL),
    private val email: String = BuildConfig.ADMIN_EMAIL,
    private val password: String = BuildConfig.ADMIN_PASSWORD,
) : ViewModel() {

    private val _uiState = MutableStateFlow<DashboardUiState>(DashboardUiState.Loading)
    val uiState: StateFlow<DashboardUiState> = _uiState.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _uiState.value = DashboardUiState.Loading
        viewModelScope.launch {
            try {
                val tokens = api.login(email, password)
                val monitors = api.getMonitors(tokens.accessToken)
                _uiState.value = DashboardUiState.Loaded(monitors)
            } catch (e: Exception) {
                _uiState.value = DashboardUiState.Error(e.message ?: "Failed to load monitors")
            }
        }
    }
}
