package com.pantawin.shared.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// Mirrors server/internal/httpapi's tokensResponse.
@Serializable
data class Tokens(
    @SerialName("access_token") val accessToken: String,
    @SerialName("refresh_token") val refreshToken: String,
)
