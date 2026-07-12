package com.pantawin.app.dashboard

import com.pantawin.shared.model.MonitorStatus

sealed interface DashboardUiState {
    data object Loading : DashboardUiState
    data class Loaded(val monitors: List<MonitorStatus>) : DashboardUiState
    data class Error(val message: String) : DashboardUiState
}
