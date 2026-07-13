package com.pantawin.app.monitors

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.outlined.Email
import androidx.compose.material.icons.outlined.Label
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.PhoneAndroid
import androidx.compose.material.icons.outlined.Timer
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddMonitorScreen(
    viewModel: AddMonitorViewModel,
    onDone: () -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.state.collectAsState()
    var name by remember { mutableStateOf("") }
    var url by remember { mutableStateOf("") }
    var interval by remember { mutableStateOf("60") }
    var emailAlerts by remember { mutableStateOf(true) }
    var pushAlerts by remember { mutableStateOf(true) }

    LaunchedEffect(state.done) {
        if (state.done) onDone()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Add monitor") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).padding(horizontal = 24.dp, vertical = 8.dp)) {
            OutlinedTextField(
                value = url,
                onValueChange = { url = it },
                label = { Text("URL") },
                placeholder = { Text("https://example.com") },
                leadingIcon = { Icon(Icons.Outlined.Link, contentDescription = null) },
                singleLine = true,
                shape = MaterialTheme.shapes.medium,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                label = { Text("Name (optional)") },
                leadingIcon = { Icon(Icons.Outlined.Label, contentDescription = null) },
                singleLine = true,
                shape = MaterialTheme.shapes.medium,
                modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
            )
            OutlinedTextField(
                value = interval,
                onValueChange = { interval = it.filter(Char::isDigit) },
                label = { Text("Check interval (seconds, min 30)") },
                leadingIcon = { Icon(Icons.Outlined.Timer, contentDescription = null) },
                singleLine = true,
                shape = MaterialTheme.shapes.medium,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
            )

            Text(
                "Alert me via",
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 20.dp, bottom = 8.dp),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ChannelChip("Email", Icons.Outlined.Email, emailAlerts) { emailAlerts = it }
                ChannelChip("Push", Icons.Outlined.PhoneAndroid, pushAlerts) { pushAlerts = it }
            }

            state.error?.let {
                Surface(
                    shape = MaterialTheme.shapes.medium,
                    color = MaterialTheme.colorScheme.errorContainer,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                ) {
                    Text(
                        it,
                        color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(12.dp),
                    )
                }
            }
            Button(
                onClick = {
                    viewModel.submit(
                        name = name,
                        url = url,
                        intervalSeconds = interval.toIntOrNull() ?: 60,
                        channels = buildList {
                            if (emailAlerts) add("email")
                            if (pushAlerts) add("push")
                        },
                    )
                },
                enabled = !state.submitting,
                shape = MaterialTheme.shapes.medium,
                modifier = Modifier.fillMaxWidth().padding(top = 24.dp).height(52.dp),
            ) {
                if (state.submitting) {
                    CircularProgressIndicator(modifier = Modifier.size(24.dp))
                } else {
                    Text("Start monitoring", style = MaterialTheme.typography.titleMedium)
                }
            }
        }
    }
}

@Composable
private fun ChannelChip(
    label: String,
    icon: ImageVector,
    selected: Boolean,
    onChange: (Boolean) -> Unit,
) {
    FilterChip(
        selected = selected,
        onClick = { onChange(!selected) },
        label = { Text(label) },
        leadingIcon = {
            Icon(
                if (selected) Icons.Filled.Check else icon,
                contentDescription = null,
                modifier = Modifier.size(18.dp),
            )
        },
    )
}
