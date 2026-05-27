package io.meshcloud.buildingblocks.runner.http

import io.meshcloud.buildingblocks.runner.StandaloneBlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.http.auth.BasicAuthInterceptor
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.OkHttpClient
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component

private val log = KotlinLogging.logger { }

/**
 * Only present in standalone mode (no kubernetes profile active). Builds an HTTP client
 * with basic auth credentials from StandaloneBlockRunnerApiConfig to fetch runs from
 * the meshObject API.
 */
@Component
@Profile("!kubernetes")
class BasicAuthHttpClientFactory(
  config: StandaloneBlockRunnerApiConfig,
) {

  private val client: OkHttpClient = OkHttpClient.Builder()
    .followRedirects(false)
    .addLogging(log)
    .addInterceptor(
      BasicAuthInterceptor(
        username = config.auth.username,
        password = config.auth.password,
      ),
    ).build()

  fun buildHttpClient(): OkHttpClient {
    return client
  }
}
