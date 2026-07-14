package com.pantawin.app.team

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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Groups
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.pantawin.shared.model.Team

/** Every team the account belongs to (M6.3), with a way to create new ones.
 * Any registered account can create a team and invite others into it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TeamsListScreen(
    viewModel: TeamsListViewModel,
    onBack: () -> Unit,
    onOpenTeam: (Team) -> Unit,
) {
    val state by viewModel.state.collectAsState()
    var showCreate by remember { mutableStateOf(false) }
    var newTeamName by remember { mutableStateOf("") }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Teams") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = { showCreate = !showCreate },
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text("Create team") },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp)) {
            if (showCreate) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                ) {
                    OutlinedTextField(
                        value = newTeamName,
                        onValueChange = { newTeamName = it },
                        label = { Text("Team name") },
                        singleLine = true,
                        shape = MaterialTheme.shapes.medium,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(
                        onClick = {
                            viewModel.create(newTeamName)
                            newTeamName = ""
                            showCreate = false
                        },
                        enabled = !state.creating && newTeamName.isNotBlank(),
                        modifier = Modifier.padding(start = 8.dp),
                    ) {
                        Text("Create")
                    }
                }
            }

            state.error?.let {
                Surface(
                    shape = MaterialTheme.shapes.medium,
                    color = MaterialTheme.colorScheme.errorContainer,
                    modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                ) {
                    Text(
                        it,
                        color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(12.dp),
                    )
                }
            }

            when {
                state.loading -> Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier.fillMaxSize(),
                ) { CircularProgressIndicator() }

                state.teams.isEmpty() -> Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    Text(
                        "No teams yet — create one to share monitors with others.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }

                else -> LazyColumn(
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                    contentPadding = PaddingValues(top = 16.dp, bottom = 96.dp),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(state.teams, key = { it.id }) { team ->
                        TeamCard(team, onClick = { onOpenTeam(team) })
                    }
                }
            }
        }
    }
}

@Composable
private fun TeamCard(team: Team, onClick: () -> Unit) {
    ElevatedCard(onClick = onClick, shape = MaterialTheme.shapes.large, modifier = Modifier.fillMaxWidth()) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth().padding(16.dp),
        ) {
            Icon(
                Icons.Filled.Groups,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.primary,
                modifier = Modifier.size(28.dp),
            )
            Text(
                team.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                modifier = Modifier.padding(start = 14.dp),
            )
        }
    }
}
