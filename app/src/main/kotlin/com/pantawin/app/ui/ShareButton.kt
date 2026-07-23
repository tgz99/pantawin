package com.pantawin.app.ui

import android.content.Context
import android.content.Intent
import android.widget.Toast
import androidx.compose.foundation.layout.Box
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.core.net.toUri

// Share affordance restricted to WhatsApp or email — not the full OS share
// sheet — per user request for incident/report sharing.
@Composable
fun ShareButton(subject: String, body: String, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    var expanded by remember { mutableStateOf(false) }

    Box(modifier) {
        IconButton(onClick = { expanded = true }) {
            Icon(Icons.Filled.Share, contentDescription = "Share")
        }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text("Share via WhatsApp") },
                onClick = {
                    expanded = false
                    shareToWhatsApp(context, body)
                },
            )
            DropdownMenuItem(
                text = { Text("Share via Email") },
                onClick = {
                    expanded = false
                    shareToEmail(context, subject, body)
                },
            )
        }
    }
}

// Targets the WhatsApp package directly (consumer, then Business) so the
// OS chooser never appears with unrelated apps.
private fun shareToWhatsApp(context: Context, body: String) {
    val intent = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, body)
    }
    val sent = listOf("com.whatsapp", "com.whatsapp.w4b").any { pkg ->
        runCatching { context.startActivity(intent.apply { setPackage(pkg) }) }.isSuccess
    }
    if (!sent) Toast.makeText(context, "WhatsApp is not installed", Toast.LENGTH_SHORT).show()
}

// ACTION_SENDTO + mailto: only resolves to apps registered for the mailto
// scheme (real mail clients) — unlike ACTION_SEND with type "message/rfc822",
// which several OEM share sheets (confirmed on the SM_M546B) still expand to
// every ACTION_SEND target (Bluetooth, Drive, WhatsApp...), not just email.
// Mirrors AboutScreen's mailto link.
private fun shareToEmail(context: Context, subject: String, body: String) {
    runCatching {
        context.startActivity(
            Intent(Intent.ACTION_SENDTO, "mailto:".toUri()).apply {
                putExtra(Intent.EXTRA_SUBJECT, subject)
                putExtra(Intent.EXTRA_TEXT, body)
            },
        )
    }.onFailure {
        Toast.makeText(context, "No email app found", Toast.LENGTH_SHORT).show()
    }
}
