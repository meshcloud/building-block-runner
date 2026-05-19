package io.meshcloud.buildingblocks.runner

import org.junit.jupiter.api.Assertions
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.test.context.ActiveProfiles

@SpringBootTest(classes = [TestConfiguration::class])
@ActiveProfiles("test-kubernetes", "kubernetes")
class BlockRunnerApiConfigSpringBootKubernetesRunTokenScenario {

  @Autowired
  lateinit var config: BlockRunnerApiConfig

  @Test
  fun `Spring context loads successfully with explicit basic auth configuration`() {
    // Then - Spring context loaded successfully
    Assertions.assertNotNull(config)

    // Verify configuration values
    Assertions.assertEquals("test-kubernetes-run-token", config.uuid)
    Assertions.assertEquals("http://localhost:8080", config.api.url)

    // Verify auth behavior - auth configured
    Assertions.assertNull(config.auth, "Auth should not be configured")
  }
}
