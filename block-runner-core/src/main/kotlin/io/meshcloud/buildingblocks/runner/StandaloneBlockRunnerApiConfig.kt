package io.meshcloud.buildingblocks.runner

import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Profile

/**
 * This configuration is only loaded in standalone mode to provide the URL and credentials
 * to actively fetch runs from the meshObject API. In Kubernetes mode neither the URL nor
 * basic auth is needed — the run object is injected directly and carries its own self-link
 * and run token for authentication.
 */
@Profile("!kubernetes")
@ConfigurationProperties(prefix = "blockrunner")
data class StandaloneBlockRunnerApiConfig(
  val api: ApiConfig,
  val auth: AuthConfig,
) {
  data class ApiConfig(
    val url: String,
  )

  data class AuthConfig(
    val username: String,
    val password: String,
  )
}
