package io.meshcloud.http.auth

import okhttp3.*

class BasicProxyAuthenticator(
  private val username: String,
  private val password: String
) : Authenticator {

  override fun authenticate(route: Route?, response: Response): Request? {
    val credentials = Credentials.basic(username, password)
    return response.request.newBuilder().header("Proxy-Authorization", credentials).build()
  }
}
