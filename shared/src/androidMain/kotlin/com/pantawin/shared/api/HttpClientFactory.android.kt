package com.pantawin.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp
import io.ktor.client.plugins.websocket.WebSockets

fun createPantawinHttpClient(): HttpClient = HttpClient(OkHttp) {
    installPantawinJson()
    install(WebSockets)
}
