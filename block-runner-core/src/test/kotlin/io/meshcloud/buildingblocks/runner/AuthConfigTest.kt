package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test

class AuthConfigTest {

  @Test
  fun `apiKey is null when not provided`() {
    val config = StandaloneBlockRunnerApiConfig.AuthConfig()
    assertNull(config.apiKey)
  }

  @Test
  fun `apiKey is null when clientId is blank`() {
    val config = StandaloneBlockRunnerApiConfig.AuthConfig(
      apiKey = StandaloneBlockRunnerApiConfig.ApiKeyConfig(clientId = "", clientSecret = "secret")
    )
    assertNull(config.apiKey)
  }

  @Test
  fun `apiKey is null when clientSecret is blank`() {
    val config = StandaloneBlockRunnerApiConfig.AuthConfig(
      apiKey = StandaloneBlockRunnerApiConfig.ApiKeyConfig(clientId = "id", clientSecret = "")
    )
    assertNull(config.apiKey)
  }

  @Test
  fun `apiKey is null when both credentials are blank`() {
    val config = StandaloneBlockRunnerApiConfig.AuthConfig(
      apiKey = StandaloneBlockRunnerApiConfig.ApiKeyConfig(clientId = "", clientSecret = "")
    )
    assertNull(config.apiKey)
  }

  @Test
  fun `apiKey is returned when both credentials are non-blank`() {
    val apiKeyConfig = StandaloneBlockRunnerApiConfig.ApiKeyConfig(clientId = "my-id", clientSecret = "my-secret")
    val config = StandaloneBlockRunnerApiConfig.AuthConfig(apiKey = apiKeyConfig)
    assertNotNull(config.apiKey)
    assertEquals("my-id", config.apiKey?.clientId)
    assertEquals("my-secret", config.apiKey?.clientSecret)
  }
}
