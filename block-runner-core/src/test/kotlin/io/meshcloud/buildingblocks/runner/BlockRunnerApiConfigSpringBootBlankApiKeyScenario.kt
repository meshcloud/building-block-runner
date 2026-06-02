package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

/**
 * Verifies that when api-key YAML placeholders resolve to empty strings (i.e. RUNNER_API_CLIENT_ID
 * and RUNNER_API_CLIENT_SECRET are not set), the [StandaloneBlockRunnerApiConfig.AuthConfig.apiKey]
 * property is null and basic auth credentials are still available.
 */
@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-blank-api-key")
class BlockRunnerApiConfigSpringBootBlankApiKeyScenario {

  @Autowired
  lateinit var standaloneConfig: StandaloneBlockRunnerApiConfig

  @Test
  fun `apiKey is null when credentials are empty strings`() {
    assertNull(
      standaloneConfig.auth.apiKey,
      "apiKey must be null when clientId and clientSecret are blank — basic auth should be used instead"
    )
  }

  @Test
  fun `basic auth credentials are still bound when api-key is blank`() {
    assertEquals("test-user", standaloneConfig.auth.username)
    assertEquals("test-password", standaloneConfig.auth.password)
  }
}
