package com.pantawin.app.team

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Email
import androidx.compose.material.icons.outlined.Groups
import androidx.compose.material.icons.outlined.HourglassEmpty
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.pantawin.shared.model.TeamMember

/**
 * Member management for one team (M6.3): any current member — not just its
 * creator — can invite teammates by email. Invited people sign in with
 * Google — no passwords to hand out.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TeamMembersScreen(
    viewModel: TeamMembersViewModel,
    teamName: String,
    onBack: () -> Unit,
) {
    val state by viewModel.state.collectAsState()
    var email by remember { mutableStateOf("") }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(teamName) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        when {
            state.loading -> Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier.fillMaxSize().padding(padding),
            ) { CircularProgressIndicator() }

            state.notMember -> Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 40.dp),
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Icon(
                        Icons.Outlined.Groups,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(48.dp),
                    )
                    Text(
                        "You're no longer a member of this team",
                        style = MaterialTheme.typography.titleMedium,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.padding(top = 16.dp),
                    )
                }
            }

            else -> Column(Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp)) {
                Text(
                    "Invited teammates sign in with Google — no passwords to share. " +
                        "This team's monitors and their alerts are visible to every member.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp),
                )
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                ) {
                    OutlinedTextField(
                        value = email,
                        onValueChange = { email = it },
                        label = { Text("Teammate's email") },
                        placeholder = { Text("name@example.com") },
                        leadingIcon = { Icon(Icons.Outlined.Email, contentDescription = null) },
                        singleLine = true,
                        shape = MaterialTheme.shapes.medium,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
                        modifier = Modifier.weight(1f),
                    )
                    FilledIconButton(
                        onClick = {
                            viewModel.invite(email)
                            email = ""
                        },
                        enabled = !state.submitting && email.isNotBlank(),
                        modifier = Modifier.padding(start = 10.dp).size(48.dp),
                    ) {
                        Icon(Icons.Filled.CheckCircle, contentDescription = "Invite")
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

                LazyColumn(
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(top = 16.dp, bottom = 24.dp),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(state.members, key = { it.email }) { member ->
                        MemberCard(member, onRemove = { viewModel.removeInvite(member.email) })
                    }
                }
            }
        }
    }
}

@Composable
private fun MemberCard(member: TeamMember, onRemove: () -> Unit) {
    ElevatedCard(shape = MaterialTheme.shapes.large, modifier = Modifier.fillMaxWidth()) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 6.dp, top = 12.dp, bottom = 12.dp),
        ) {
            Column(Modifier.weight(1f)) {
                Text(
                    member.email,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Medium,
                )
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 4.dp)) {
                    val (icon, label, tint) = if (member.joined) {
                        Triple(Icons.Filled.CheckCircle, "Joined", MaterialTheme.colorScheme.primary)
                    } else {
                        Triple(Icons.Outlined.HourglassEmpty, "Invited — waiting for first sign-in", MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(14.dp))
                    Text(
                        label,
                        style = MaterialTheme.typography.labelMedium,
                        color = tint,
                        modifier = Modifier.padding(start = 4.dp),
                    )
                }
            }
            if (!member.joined) {
                IconButton(onClick = onRemove) {
                    Icon(
                        Icons.Outlined.Close,
                        contentDescription = "Withdraw invite",
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}
