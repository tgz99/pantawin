package com.pantawin.app

import android.app.Application
import com.pantawin.app.data.SessionManager
import com.pantawin.shared.api.PantawinApiClient
import com.pantawin.shared.api.createPantawinHttpClient

/**
 * Application-scoped manual DI container (M1). The SessionManager and API
 * client live for the process lifetime; ViewModels read them via
 * (application as PantawinApp).sessionManager. Hilt is a later refactor.
 */
class PantawinApp : Application() {
    val sessionManager: SessionManager by lazy {
        SessionManager(
            context = this,
            api = PantawinApiClient(createPantawinHttpClient(), BuildConfig.API_BASE_URL),
        )
    }
}
