package com.pantawin.app.monitors

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.app.ui.EmptyState
import com.pantawin.app.ui.ErrorState
import com.pantawin.app.ui.LoadingState
import com.pantawin.app.ui.theme.StatusDown
import com.pantawin.app.ui.theme.StatusUp
import com.pantawin.shared.model.Incident
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

// M5: full incident history for one monitor, newest first. Rows expand into
// a detail dialog; timestamps render in the device zone (WIB) over the
// API's UTC instants.

data class IncidentHistoryState(
    val incidents: List<Incident> = emptyList(),
    val loading: Boolean = true,
    val error: String? = null,
)

class IncidentHistoryViewModel(
    private val gateway: MonitorGateway,
    private val monitorId: Long,
) : ViewModel() {
    private val _state = MutableStateFlow(IncidentHistoryState())
    val state = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            runCatching { gateway.incidents(monitorId) }
                .onSuccess { _state.value = IncidentHistoryState(incidents = it, loading = false) }
                .onFailure {
                    _state.value = _state.value.copy(
                        loading = false, error = it.message ?: "Failed to load incidents",
                    )
                }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun IncidentHistoryScreen(
    viewModel: IncidentHistoryViewModel,
    onBack: () -> Unit,
) {
    val state by viewModel.state.collectAsState()
    var selected by rememberSaveable { mutableStateOf<Long?>(null) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Incident history") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        when {
            state.loading -> LoadingState(Modifier.padding(padding))
            state.error != null ->
                ErrorState(state.error ?: "", onRetry = viewModel::load, modifier = Modifier.padding(padding))
            state.incidents.isEmpty() ->
                EmptyState(
                    title = "No incidents",
                    subtitle = "This monitor has never been down. Long may it last.",
                    modifier = Modifier.padding(padding),
                )
            else -> LazyColumn(
                contentPadding = PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.padding(padding).fillMaxSize(),
            ) {
                items(state.incidents, key = { it.id }) { incident ->
                    ElevatedCard(shape = MaterialTheme.shapes.large) {
                        IncidentRow(
                            incident = incident,
                            modifier = Modifier
                                .clickable { selected = incident.id }
                                .padding(horizontal = 16.dp, vertical = 12.dp),
                        )
                    }
                }
            }
        }
    }

    state.incidents.firstOrNull { it.id == selected }?.let { incident ->
        IncidentDetailDialog(incident = incident, onDismiss = { selected = null })
    }
}

// One history row: status dot, cause + start time, duration on the right.
// Shared with the Monitor Detail screen's "Recent incidents" card.
@Composable
fun IncidentRow(incident: Incident, modifier: Modifier = Modifier) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = modifier.fillMaxWidth()) {
        Surface(
            color = if (incident.ongoing) StatusDown else StatusUp,
            shape = CircleShape,
            content = {},
            modifier = Modifier.size(10.dp),
        )
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(
                incident.cause,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                formatLocal(incident.startedAt),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            incident.durationS?.let { humanDuration(it) } ?: "ongoing",
            style = MaterialTheme.typography.labelLarge,
            color = if (incident.ongoing) StatusDown else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
fun IncidentDetailDialog(incident: Incident, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text(if (incident.ongoing) "Ongoing incident" else "Resolved incident") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                DialogLine("Cause", incident.cause)
                DialogLine("Started", formatLocal(incident.startedAt))
                DialogLine("Resolved", incident.resolvedAt?.let { formatLocal(it) } ?: "not yet")
                DialogLine(
                    "Duration",
                    incident.durationS?.let { humanDuration(it) }
                        ?: humanDuration(elapsedSince(incident.startedAt)) + " so far",
                )
            }
        },
    )
}

@Composable
private fun DialogLine(label: String, value: String) {
    Row {
        Text(
            label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(0.4f),
        )
        Box(Modifier.weight(0.6f)) {
            Text(value, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.Medium)
        }
    }
}

// "13 Jul, 23:41" in the device zone — WIB rendering over UTC storage.
private val localFormat = DateTimeFormatter.ofPattern("d MMM, HH:mm").withZone(ZoneId.systemDefault())

internal fun formatLocal(utcInstant: String): String =
    runCatching { localFormat.format(Instant.parse(utcInstant)) }.getOrDefault(utcInstant)

internal fun elapsedSince(utcInstant: String): Long =
    runCatching { Duration.between(Instant.parse(utcInstant), Instant.now()).seconds }
        .getOrDefault(0L)
        .coerceAtLeast(0L)

internal fun humanDuration(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3600 -> "${seconds / 60}m ${seconds % 60}s"
    seconds < 86400 -> "${seconds / 3600}h ${(seconds % 3600) / 60}m"
    else -> "${seconds / 86400}d ${(seconds % 86400) / 3600}h"
}
