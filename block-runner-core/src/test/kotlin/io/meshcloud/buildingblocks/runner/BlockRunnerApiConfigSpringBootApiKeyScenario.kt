package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-api-key")
class BlockRunnerApiConfigSpringBootApiKeyScenario {

  @Autowired
  lateinit var config: BlockRunnerApiConfig

  @Autowired
  lateinit var standaloneConfig: StandaloneBlockRunnerApiConfig

  @Test
  fun `Spring context loads successfully with API key configuration`() {
    assertNotNull(config)
    assertEquals("test-uuid-api-key", config.uuid)

    assertNotNull(standaloneConfig, "StandaloneBlockRunnerApiConfig should be present in non-kubernetes mode")
    assertEquals("http://localhost:8080", standaloneConfig.api.url)
  }

  @Test
  fun `API key credentials are bound correctly`() {
    val apiKey = standaloneConfig.auth.apiKey
    assertNotNull(apiKey, "auth.apiKey must be present when configured")
    assertEquals("test-client-id", apiKey!!.clientId)
    assertEquals("test-client-secret", apiKey.clientSecret)
  }

  @Test
  fun `username and password are null when only API key is configured`() {
    assertNull(standaloneConfig.auth.username, "username should be null when API key auth is used")
    assertNull(standaloneConfig.auth.password, "password should be null when API key auth is used")
  }
}
