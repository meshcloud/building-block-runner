package io.meshcloud.buildingblocks.runner.http

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.http.addLogging
import io.meshcloud.http.auth.BasicAuthInterceptor
import mu.KotlinLogging
import okhttp3.OkHttpClient
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component

private val log = KotlinLogging.logger { }

/**
 * When this component is build (no kubernetes profile active) the configuration MUST have
 * a basic auth user set in order to fetch runs. Mostly useful for local testing.
 */
@Component
@Profile("!kubernetes")
class BasicAuthHttpClientFactory(
  config: BlockRunnerApiConfig
) {

  private val client: OkHttpClient

  init {
    val auth = config.auth
      ?: throw IllegalStateException("Basic auth (username and password) config is not present")

    client = OkHttpClient.Builder()
      .followRedirects(false)
      .addLogging(log)
      .addInterceptor(
        BasicAuthInterceptor(
          username = auth.username,
          password = auth.password
        )
      ).build()
  }

  fun buildHttpClient(): OkHttpClient {
    return client
  }
}
