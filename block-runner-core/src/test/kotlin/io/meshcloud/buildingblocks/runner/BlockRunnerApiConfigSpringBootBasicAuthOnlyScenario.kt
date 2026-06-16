package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions.*
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

/**
 * Real Spring Boot integration tests that verify BlockRunnerApiConfig and
 * StandaloneBlockRunnerApiConfig can be correctly bound from YAML configuration
 * using Spring's @ConfigurationProperties mechanism.
 *
 * These tests actually start a Spring Boot context with different profiles to ensure
 * the configuration binding works correctly in a real Spring environment.
 */
@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-basic-auth")
class BlockRunnerApiConfigSpringBootBasicAuthOnlyScenario {

  @Autowired
  lateinit var config: BlockRunnerApiConfig

  @Autowired
  lateinit var standaloneConfig: StandaloneBlockRunnerApiConfig

  @Test
  fun `Spring context loads successfully with basic auth only configuration`() {
    // Then - Spring context loaded successfully
    assertNotNull(config)

    // Verify shared configuration values
    assertEquals("test-uuid-basic-auth", config.uuid)
    assertEquals("test-version-basic-auth", config.version)

    // Verify standalone URL config is present and bound correctly
    assertNotNull(standaloneConfig, "StandaloneBlockRunnerApiConfig should be present in non-kubernetes mode")
    assertEquals("http://localhost:8080", standaloneConfig.api.url)

    // Verify auth is on the standalone config and bound correctly
    assertEquals("test-user", standaloneConfig.auth.username)
    assertEquals("test-password", standaloneConfig.auth.password)
  }
}

