package com.pantawin.app.monitors

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Logout
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.pantawin.app.ui.EmptyState
import com.pantawin.app.ui.ErrorState
import com.pantawin.app.ui.LoadingState
import com.pantawin.app.ui.visual
import com.pantawin.shared.model.MonitorState
import com.pantawin.shared.model.MonitorStatus

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MonitorsScreen(
    viewModel: MonitorsViewModel,
    onAdd: () -> Unit,
    onLogout: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Pantawin") },
                actions = {
                    IconButton(onClick = onLogout) {
                        Icon(Icons.Filled.Logout, contentDescription = "Log out")
                    }
                },
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = onAdd) {
                Icon(Icons.Filled.Add, contentDescription = "Add monitor")
            }
        },
    ) { padding ->
        when (val s = state) {
            is MonitorsUiState.Loading -> LoadingState(Modifier.padding(padding))
            is MonitorsUiState.Error -> ErrorState(s.message, onRetry = viewModel::refresh, modifier = Modifier.padding(padding))
            is MonitorsUiState.Loaded ->
                if (s.monitors.isEmpty()) {
                    EmptyState(
                        title = "No monitors yet",
                        subtitle = "Tap + to add your first monitor and Pantawin will start watching it.",
                        modifier = Modifier.padding(padding),
                    )
                } else {
                    LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                        items(s.monitors, key = { it.id }) { m ->
                            MonitorRow(
                                monitor = m,
                                onPause = { viewModel.pause(m.id) },
                                onResume = { viewModel.resume(m.id) },
                                onDelete = { viewModel.delete(m.id) },
                            )
                            HorizontalDivider()
                        }
                    }
                }
        }
    }
}

@Composable
private fun MonitorRow(
    monitor: MonitorStatus,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onDelete: () -> Unit,
) {
    val visual = monitor.status.visual()
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            visual.icon,
            contentDescription = visual.label,
            tint = visual.color,
            modifier = Modifier.size(28.dp),
        )
        Column(Modifier.weight(1f).padding(start = 12.dp)) {
            Text(monitor.name, style = MaterialTheme.typography.titleMedium)
            Text(monitor.url, style = MaterialTheme.typography.bodySmall)
            val detail = buildString {
                append(visual.label)
                monitor.responseTimeMs?.let { append(" · ${it}ms") }
            }
            Text(detail, style = MaterialTheme.typography.bodySmall, color = visual.color)
        }
        if (monitor.status == MonitorState.PAUSED) {
            IconButton(onClick = onResume) {
                Icon(Icons.Filled.PlayArrow, contentDescription = "Resume")
            }
        } else {
            IconButton(onClick = onPause) {
                Icon(Icons.Filled.Pause, contentDescription = "Pause")
            }
        }
        IconButton(onClick = onDelete) {
            Icon(Icons.Filled.Delete, contentDescription = "Delete")
        }
    }
}
