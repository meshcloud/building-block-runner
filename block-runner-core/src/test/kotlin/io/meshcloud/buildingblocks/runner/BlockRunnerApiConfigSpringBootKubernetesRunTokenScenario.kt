package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

/**
 * Verifies that in Kubernetes mode:
 * - BlockRunnerApiConfig is bound with only uuid (no api.url required)
 * - StandaloneBlockRunnerApiConfig is NOT present in the context because the
 *   kubernetes profile excludes it via @Profile("!kubernetes")
 */
@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-kubernetes", "kubernetes")
class BlockRunnerApiConfigSpringBootKubernetesRunTokenScenario {

  @Autowired
  lateinit var config: BlockRunnerApiConfig

  /**
   * Must be nullable — in kubernetes mode this bean must NOT be created.
   */
  @Autowired(required = false)
  var standaloneConfig: StandaloneBlockRunnerApiConfig? = null

  @Test
  fun `Spring context loads successfully without api url in Kubernetes mode`() {
    // Then - Spring context loaded successfully
    Assertions.assertNotNull(config)

    // Verify shared config is bound
    Assertions.assertEquals("test-kubernetes-run-token", config.uuid)

    // Critical: StandaloneBlockRunnerApiConfig must NOT be present — api.url and auth
    // are not needed in kubernetes mode because the run object provides its own self-link
    // and run token for authentication
    Assertions.assertNull(
      standaloneConfig,
      "StandaloneBlockRunnerApiConfig must not be present when kubernetes profile is active",
    )
  }
}
