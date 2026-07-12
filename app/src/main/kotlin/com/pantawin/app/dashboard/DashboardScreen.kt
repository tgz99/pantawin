package com.pantawin.app.dashboard

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
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
import androidx.lifecycle.viewmodel.compose.viewModel
import com.pantawin.shared.model.MonitorState
import com.pantawin.shared.model.MonitorStatus

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DashboardScreen(viewModel: DashboardViewModel = viewModel()) {
    val state by viewModel.uiState.collectAsState()

    Scaffold(
        topBar = { TopAppBar(title = { Text("Pantawin") }) },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when (val s = state) {
                is DashboardUiState.Loading -> LoadingContent()
                is DashboardUiState.Loaded -> MonitorList(s.monitors)
                is DashboardUiState.Error -> ErrorContent(s.message, onRetry = viewModel::refresh)
            }
        }
    }
}

@Composable
private fun LoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text("Couldn't load monitors", style = MaterialTheme.typography.titleMedium)
        Text(message, style = MaterialTheme.typography.bodyMedium)
        Button(onClick = onRetry, modifier = Modifier.padding(top = 16.dp)) {
            Text("Retry")
        }
    }
}

@Composable
private fun MonitorList(monitors: List<MonitorStatus>) {
    if (monitors.isEmpty()) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text("No monitors yet")
        }
        return
    }
    LazyColumn(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        items(monitors, key = { it.id }) { monitor ->
            MonitorRow(monitor)
        }
    }
}

@Composable
private fun MonitorRow(monitor: MonitorStatus) {
    Column(modifier = Modifier.padding(vertical = 12.dp)) {
        Text(monitor.name, style = MaterialTheme.typography.titleMedium)
        Text(monitor.url, style = MaterialTheme.typography.bodySmall)
        Text(
            statusLabel(monitor.status),
            style = MaterialTheme.typography.bodyLarge,
        )
    }
}

private fun statusLabel(status: MonitorState): String = when (status) {
    MonitorState.UP -> "UP"
    MonitorState.DOWN -> "DOWN"
    MonitorState.PAUSED -> "PAUSED"
    MonitorState.PENDING -> "PENDING"
}
