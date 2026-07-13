package com.pantawin.app.ui

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Error
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.Pending
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import com.pantawin.app.ui.theme.StatusDown
import com.pantawin.app.ui.theme.StatusPaused
import com.pantawin.app.ui.theme.StatusPending
import com.pantawin.app.ui.theme.StatusUp
import com.pantawin.shared.model.MonitorState

// Status is always icon + color + text label — never color alone (spec 6.6,
// accessibility). This maps a state to all three.
data class StatusVisual(val label: String, val color: Color, val icon: ImageVector)

fun MonitorState.visual(): StatusVisual = when (this) {
    MonitorState.UP -> StatusVisual("Up", StatusUp, Icons.Filled.CheckCircle)
    MonitorState.DOWN -> StatusVisual("Down", StatusDown, Icons.Filled.Error)
    MonitorState.PAUSED -> StatusVisual("Paused", StatusPaused, Icons.Filled.Pause)
    MonitorState.PENDING -> StatusVisual("Pending", StatusPending, Icons.Filled.Pending)
}
