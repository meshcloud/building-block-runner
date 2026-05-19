package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.SingleShotRunner
import mu.KotlinLogging
import org.junit.jupiter.api.Test
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.stereotype.Component
import org.springframework.test.context.ActiveProfiles

private val log = KotlinLogging.logger { }

@SpringBootTest
@ActiveProfiles("kubernetes")
@Import(AzureDevOpsRunnerKubernetesStartupScenario.KubernetesTestConfiguration::class)
class AzureDevOpsRunnerKubernetesStartupScenario {

  @TestConfiguration
  class KubernetesTestConfiguration {

    @Component
    @Primary
    class TestRunTerminator : SingleShotRunner.RunTerminator {
      override fun exit(exitCode: Int) {
        log.info { "Exit with code: $exitCode" }
      }
    }
  }

  @Test
  fun `spring boot can start the app`() {
    // no op
  }
}
