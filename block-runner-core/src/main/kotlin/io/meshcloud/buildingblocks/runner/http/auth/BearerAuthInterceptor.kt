package io.meshcloud.buildingblocks.runner.http.auth

import okhttp3.Interceptor
import okhttp3.Response

/**
 * Will simply add a constant token. No retry logic in place. Auth will fail if token
 * is not valid anymore.
 */
class BearerAuthInterceptor(
  private val token: String,
) : Interceptor {

  override fun intercept(chain: Interceptor.Chain): Response {
    val request = chain.request()
    val builder = request.newBuilder()
      .header("Authorization", "Bearer $token")

    return chain.proceed(builder.build())
  }
}
