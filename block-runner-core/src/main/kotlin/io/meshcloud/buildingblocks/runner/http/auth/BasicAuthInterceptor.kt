package io.meshcloud.buildingblocks.runner.http.auth

import okhttp3.*

class BasicAuthInterceptor(
  username: String,
  password: String
) : Interceptor {

  private val credentials = Credentials.basic(username, password)

  override fun intercept(chain: Interceptor.Chain): Response {
    val request = chain.request()
    val builder = request.newBuilder()
      .header("Authorization", credentials)

    return chain.proceed(builder.build())
  }
}
