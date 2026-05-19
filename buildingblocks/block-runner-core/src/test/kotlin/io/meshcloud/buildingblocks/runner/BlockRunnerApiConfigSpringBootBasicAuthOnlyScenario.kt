package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

/**
 * Real Spring Boot integration tests that verify BlockRunnerApiConfig can be correctly
 * bound from YAML configuration using Spring's @ConfigurationProperties mechanism.
 *
 * These tests actually start a Spring Boot context with different profiles to ensure
 * the configuration binding works correctly in a real Spring environment.
 */
@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-basic-auth")
class BlockRunnerApiConfigSpringBootBasicAuthOnlyScenario {

  @Autowired
  lateinit var config: BlockRunnerApiConfig

  @Test
  fun `Spring context loads successfully with basic auth only configuration`() {
    // Then - Spring context loaded successfully
    assertNotNull(config)

    // Verify configuration values
    assertEquals("test-uuid-basic-auth", config.uuid)
    assertEquals("http://localhost:8080", config.api.url)

    // Verify auth behavior - basic auth provided
    assertNotNull(config.auth, "Auth should be configured")
    assertEquals("test-user", config.auth?.username)
    assertEquals("test-password", config.auth?.password)
  }
}

