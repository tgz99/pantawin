package com.pantawin.shared.api

import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp

fun createPantawinHttpClient(): HttpClient = HttpClient(OkHttp) {
    installPantawinJson()
}
