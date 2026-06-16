package io.meshcloud.buildingblocks.runner.http

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.StandaloneBlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.http.auth.ApiKeyAuthInterceptor
import io.meshcloud.buildingblocks.runner.http.auth.BasicAuthInterceptor
import okhttp3.OkHttpClient
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component

private val log = KotlinLogging.logger { }

/**
 * Only present in standalone mode (no kubernetes profile active). Builds an HTTP client
 * configured for authentication against the meshObject API.
 *
 * If [StandaloneBlockRunnerApiConfig.AuthConfig.apiKey] is present, the client uses
 * [ApiKeyAuthInterceptor] which exchanges the API key credentials for a short-lived Bearer token
 * via POST /api/login, caching and refreshing it automatically.
 *
 * Otherwise the client falls back to HTTP Basic auth using [username] and [password] — preserving
 * backwards compatibility with existing deployments.
 */
@Component
@Profile("!kubernetes")
class AuthHttpClientFactory(
  config: StandaloneBlockRunnerApiConfig,
  blockRunnerConfig: BlockRunnerApiConfig
) {

  private val client: OkHttpClient

  init {
    client = OkHttpClient.Builder()
      .followRedirects(false)
      .addRunnerVersionHeader(blockRunnerConfig.version)
      .addLogging(log)
      .addInterceptor(buildAuthInterceptor(config)).build()
  }

  fun buildHttpClient(): OkHttpClient {
    return client
  }

  private fun buildAuthInterceptor(config: StandaloneBlockRunnerApiConfig) =
    if (config.auth.apiKey != null) {
      log.info { "Using API key authentication for meshStack API" }
      ApiKeyAuthInterceptor(
        baseUrl = config.api.url,
        clientId = config.auth.apiKey.clientId,
        clientSecret = config.auth.apiKey.clientSecret,
      )
    } else {
      /**
       * @deprecated Basic auth is deprecated and should not be used for new deployments.
       * It is still supported for backwards compatibility, but will eventually be removed in favor of API key authentication.
       */
      log.info { "Using Basic authentication for meshStack API" }
      BasicAuthInterceptor(
        username = requireNotNull(config.auth.username) {
          "blockrunner.auth.username must be set when blockrunner.auth.api-key is not configured"
        },
        password = requireNotNull(config.auth.password) {
          "blockrunner.auth.password must be set when blockrunner.auth.api-key is not configured"
        },
      )
    }

  private fun OkHttpClient.Builder.addRunnerVersionHeader(version: String): OkHttpClient.Builder = apply {
    addInterceptor { chain ->
      val requestWithRunnerVersion = chain.request().newBuilder()
        .header(RUNNER_VERSION_HEADER, version)
        .build()

      chain.proceed(requestWithRunnerVersion)
    }
  }

  companion object {
    private const val RUNNER_VERSION_HEADER = "X-Meshcloud-Runner-Version"
  }
}

