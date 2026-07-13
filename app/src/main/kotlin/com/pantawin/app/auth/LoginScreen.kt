package com.pantawin.app.auth

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.text.KeyboardOptions
import androidx.credentials.CredentialManager
import androidx.credentials.CustomCredential
import androidx.credentials.GetCredentialRequest
import androidx.credentials.exceptions.GetCredentialCancellationException
import androidx.credentials.exceptions.GetCredentialException
import com.google.android.libraries.identity.googleid.GetGoogleIdOption
import com.google.android.libraries.identity.googleid.GoogleIdTokenCredential
import kotlinx.coroutines.launch
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MonitorHeart
import androidx.compose.material.icons.outlined.Email
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp

@Composable
fun LoginScreen(viewModel: AuthViewModel) {
    val state by viewModel.state.collectAsState()
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var showPassword by remember { mutableStateOf(false) }

    Column(
        modifier = Modifier.fillMaxSize().padding(horizontal = 28.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        // Brand mark — mirrors the launcher's pulse identity.
        Surface(
            shape = MaterialTheme.shapes.extraLarge,
            color = MaterialTheme.colorScheme.primary,
        ) {
            Icon(
                Icons.Filled.MonitorHeart,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimary,
                modifier = Modifier.padding(18.dp).size(36.dp),
            )
        }
        Text(
            "Pantawin",
            style = MaterialTheme.typography.headlineLarge,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.padding(top = 20.dp),
        )
        Text(
            "Know the moment your site goes down",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 4.dp, bottom = 32.dp),
        )
        OutlinedTextField(
            value = email,
            onValueChange = { email = it },
            label = { Text("Email") },
            leadingIcon = { Icon(Icons.Outlined.Email, contentDescription = null) },
            singleLine = true,
            shape = MaterialTheme.shapes.medium,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = password,
            onValueChange = { password = it },
            label = { Text("Password") },
            leadingIcon = { Icon(Icons.Outlined.Lock, contentDescription = null) },
            trailingIcon = {
                IconButton(onClick = { showPassword = !showPassword }) {
                    Icon(
                        if (showPassword) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility,
                        contentDescription = if (showPassword) "Hide password" else "Show password",
                    )
                }
            },
            singleLine = true,
            shape = MaterialTheme.shapes.medium,
            visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
        )
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
            onClick = { viewModel.login(email, password) },
            enabled = !state.submitting,
            shape = MaterialTheme.shapes.medium,
            modifier = Modifier.fillMaxWidth().padding(top = 24.dp).height(52.dp),
        ) {
            if (state.submitting) {
                CircularProgressIndicator(modifier = Modifier.size(24.dp))
            } else {
                Text("Sign in", style = MaterialTheme.typography.titleMedium)
            }
        }
        GoogleSignInButton(viewModel = viewModel, enabled = !state.submitting)
    }
}

// "Continue with Google" via Credential Manager. Renders only when the
// google-services plugin generated a default_web_client_id (i.e. Google
// sign-in is enabled in the Firebase project) — dormant otherwise, matching
// the server's GOOGLE_CLIENT_ID gate.
@Composable
private fun GoogleSignInButton(viewModel: AuthViewModel, enabled: Boolean) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val webClientIdRes = remember {
        context.resources.getIdentifier("default_web_client_id", "string", context.packageName)
    }
    if (webClientIdRes == 0) return

    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth().padding(top = 20.dp),
    ) {
        HorizontalDivider(Modifier.weight(1f))
        Text(
            "or",
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 12.dp),
        )
        HorizontalDivider(Modifier.weight(1f))
    }
    OutlinedButton(
        onClick = {
            scope.launch {
                try {
                    val option = GetGoogleIdOption.Builder()
                        .setServerClientId(context.getString(webClientIdRes))
                        // Show all Google accounts, not only previously used ones.
                        .setFilterByAuthorizedAccounts(false)
                        .build()
                    val request = GetCredentialRequest.Builder()
                        .addCredentialOption(option)
                        .build()
                    val result = CredentialManager.create(context)
                        .getCredential(context, request)
                    val credential = result.credential
                    if (credential is CustomCredential &&
                        credential.type == GoogleIdTokenCredential.TYPE_GOOGLE_ID_TOKEN_CREDENTIAL
                    ) {
                        val idToken = GoogleIdTokenCredential.createFrom(credential.data).idToken
                        viewModel.loginWithGoogle(idToken)
                    } else {
                        viewModel.googleSignInFailed("Unexpected credential type")
                    }
                } catch (e: GetCredentialCancellationException) {
                    viewModel.googleSignInFailed(null) // user dismissed — not an error
                } catch (e: GetCredentialException) {
                    viewModel.googleSignInFailed(e.message ?: "Google sign-in unavailable")
                }
            }
        },
        enabled = enabled,
        shape = MaterialTheme.shapes.medium,
        modifier = Modifier.fillMaxWidth().padding(top = 20.dp).height(52.dp),
    ) {
        Text("Continue with Google", style = MaterialTheme.typography.titleMedium)
    }
}
