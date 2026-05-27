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

  /**
   * Authentication configuration. Exactly one of [apiKey] or ([username] + [password]) must be set.
   * If [apiKey] is configured it takes precedence and a short-lived Bearer token is obtained via
   * POST /api/login. Otherwise, HTTP Basic auth with [username]/[password] is used.
   */
  data class AuthConfig(
    val username: String? = null,
    val password: String? = null,
    val apiKey: ApiKeyConfig? = null,
  )

  /**
   * Credentials for the meshStack API key login flow.
   * The runner exchanges [clientId]/[clientSecret] for a short-lived Bearer token.
   */
  data class ApiKeyConfig(
    val clientId: String,
    val clientSecret: String,
  )
}
