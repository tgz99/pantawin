package com.pantawin.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import com.pantawin.app.navigation.PantawinNavHost
import com.pantawin.app.ui.theme.PantawinTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Only when launched by a DOWN alert's full-screen intent: show over
        // the lock screen and wake it, like an incoming call. Gated on this
        // extra so a normal app launch/notification tap never forces this.
        if (intent?.getBooleanExtra(EXTRA_ALERT, false) == true) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        }
        enableEdgeToEdge()
        val session = (application as PantawinApp).sessionManager
        setContent {
            PantawinTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    PantawinNavHost(session)
                }
            }
        }
    }

    companion object {
        /** Set on the intent that backs a DOWN alert's full-screen PendingIntent. */
        const val EXTRA_ALERT = "com.pantawin.app.EXTRA_ALERT"
    }
}
