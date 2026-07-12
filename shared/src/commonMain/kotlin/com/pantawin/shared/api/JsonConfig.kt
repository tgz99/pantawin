package com.pantawin.shared.api

import io.ktor.client.HttpClientConfig
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.serialization.kotlinx.json.json
import kotlinx.serialization.json.Json

/** Installed by every platform's HttpClient factory so JSON (de)serialization is consistent. */
fun HttpClientConfig<*>.installPantawinJson() {
    install(ContentNegotiation) {
        json(Json { ignoreUnknownKeys = true })
    }
}
