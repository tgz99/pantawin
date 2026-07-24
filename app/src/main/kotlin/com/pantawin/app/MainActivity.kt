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
}
