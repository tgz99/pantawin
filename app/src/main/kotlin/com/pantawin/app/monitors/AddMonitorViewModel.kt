package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.MonitorInput
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class AddMonitorState(
    val submitting: Boolean = false,
    val error: String? = null,
    val done: Boolean = false,
)

class AddMonitorViewModel(private val gateway: MonitorGateway) : ViewModel() {

    private val _state = MutableStateFlow(AddMonitorState())
    val state: StateFlow<AddMonitorState> = _state.asStateFlow()

    fun submit(name: String, url: String, intervalSeconds: Int) {
        // Client-side pre-check for fast feedback; the server's SSRF guard is
        // the actual authority.
        val trimmedUrl = url.trim()
        if (!trimmedUrl.startsWith("http://") && !trimmedUrl.startsWith("https://")) {
            _state.value = AddMonitorState(error = "URL must start with http:// or https://")
            return
        }
        _state.value = AddMonitorState(submitting = true)
        viewModelScope.launch {
            runCatching {
                gateway.create(
                    MonitorInput(
                        name = name.trim().ifBlank { null },
                        url = trimmedUrl,
                        intervalSeconds = intervalSeconds,
                    ),
                )
            }.onSuccess {
                _state.value = AddMonitorState(done = true)
            }.onFailure {
                _state.value = AddMonitorState(error = it.message ?: "Failed to add monitor")
            }
        }
    }
}
