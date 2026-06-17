package io.meshcloud.buildingblocks.runner.http.auth

import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.time.Duration
import java.time.Instant
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

private val log = KotlinLogging.logger {}

private val MEDIA_TYPE_JSON = "application/json; charset=utf-8".toMediaType()

/**
 * OkHttp interceptor that obtains a short-lived Bearer token from the meshStack API key
 * login endpoint (POST /api/login) and caches it until it nears expiry.
 *
 * Thread-safe. Uses a [ReentrantLock] with double-checked locking so that only one thread
 * fetches a new token at a time while other threads continue to use the cached token.
 *
 * On login failure the interceptor logs at ERROR level with the HTTP status code and throws
 * an [IOException] whose message contains the status — ensuring both the local log entry and
 * any upstream `catch (ex: Exception) { log.error(ex) }` blocks show a meaningful message.
 *
 * @param baseUrl  The base URL of the meshStack API (e.g. `https://example.meshstack.io`).
 * @param clientId The API key client ID.
 * @param clientSecret The API key client secret.
 * @param loginHttpClient OkHttpClient used exclusively for the login call. A separate instance
 *   is required to avoid a recursive interceptor chain. Injectable for testing.
 */
class ApiKeyAuthInterceptor(
  private val baseUrl: String,
  private val clientId: String,
  private val clientSecret: String,
  private val loginHttpClient: OkHttpClient = OkHttpClient.Builder()
    .followRedirects(false)
    .build(),
) : Interceptor {

  private data class ApiKeyLoginRequest(
    val clientId: String,
    val clientSecret: String,
  )

  private data class ApiKeyLoginResponse(
    @JsonProperty("access_token") val accessToken: String,
    @JsonProperty("expires_in") val expiresIn: Int,
  )

  private val mapper = jacksonObjectMapper().apply {
    configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
  }

  private val lock = ReentrantLock()

  @Volatile
  private var cachedToken: CachedToken? = null

  private data class CachedToken(val token: String, val expiresAt: Instant)

  companion object {
    private val TOKEN_EXPIRY_BUFFER: Duration = Duration.ofSeconds(30)
    private val MIN_TOKEN_LIFETIME: Duration = Duration.ofSeconds(1)
  }

  override fun intercept(chain: Interceptor.Chain): Response {
    val token = getValidToken()
    val request = chain.request().newBuilder()
      .header("Authorization", "Bearer $token")
      .build()
    return chain.proceed(request)
  }

  private fun getValidToken(): String {
    // Fast path: cached token is still valid (no lock needed for a simple read).
    val current = cachedToken
    if (current != null && Instant.now().isBefore(current.expiresAt)) {
      return current.token
    }

    // Slow path: fetch a fresh token under the lock.
    return lock.withLock {
      // Re-check after acquiring the lock (another thread may have refreshed it already).
      val locked = cachedToken
      if (locked != null && Instant.now().isBefore(locked.expiresAt)) {
        locked.token
      } else {
        fetchToken()
      }
    }
  }

  /**
   * Performs POST /api/login and updates [cachedToken] / [expiresAt].
   * Must be called while holding [lock].
   *
   * @throws IOException if the login request fails or the server returns a non-200 status.
   */
  private fun fetchToken(): String {
    val loginUrl = "$baseUrl/api/login"

    val requestBody = mapper.writeValueAsString(
      ApiKeyLoginRequest(clientId = clientId, clientSecret = clientSecret),
    ).toRequestBody(MEDIA_TYPE_JSON)

    val request = Request.Builder()
      .url(loginUrl)
      .post(requestBody)
      .header("Accept", "application/json")
      .build()

    val response = loginHttpClient.newCall(request).execute()
    response.use {
      if (!response.isSuccessful) {
        val body = response.body.string()
        val msg = "API key login failed with HTTP ${response.code}: $body"
        log.error { msg }
        throw IOException(msg)
      }

      val loginResponse: ApiKeyLoginResponse = mapper.readValue(
        response.body.string(),
      )

      val lifetime = Duration.ofSeconds(loginResponse.expiresIn.toLong()) - TOKEN_EXPIRY_BUFFER
      val effectiveLifetime = if (lifetime <= Duration.ZERO) MIN_TOKEN_LIFETIME else lifetime

      cachedToken = CachedToken(
        token = loginResponse.accessToken,
        expiresAt = Instant.now().plus(effectiveLifetime),
      )

      log.debug { "Obtained new API key access token, valid for ${effectiveLifetime.toSeconds()}s" }

      return loginResponse.accessToken
    }
  }
}
