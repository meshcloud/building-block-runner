package io.meshcloud.buildingblocks.runner

import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.github.oshai.kotlinlogging.KotlinLogging
import org.springframework.boot.autoconfigure.EnableAutoConfiguration
import org.springframework.boot.context.properties.ConfigurationPropertiesScan
import org.springframework.context.annotation.ComponentScan
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Primary
import org.springframework.stereotype.Component
import org.springframework.stereotype.Service

private val log = KotlinLogging.logger { }

/**
 * This is more a library than a full-blown spring boot app. But we test if a context comes up
 * testing the configuration class. In order to make this work we need some basic spring setup
 * which would normally be found in the non-test context.
 * It needs to mock certain classes which would naturally only be present in a consumer of this
 * module. However, as we only want to test the generel availability to boot/bring up a Spring context
 * with the various expected configs this is irrelevant for this test and just need to be present
 * so it has a chance to work.
 * Specialized scenarios should be present in the runner implementations.
 */
@Configuration
@EnableAutoConfiguration
@ConfigurationPropertiesScan(
  basePackages = [
    "io.meshcloud.buildingblocks.runner",
  ]
)
@ComponentScan(
  basePackages = [
    "io.meshcloud.buildingblocks.runner",
  ]
)
class TestConfiguration {

  @Service
  class NoOpBlockRunnerService : BlockRunnerService {
    override fun processBlock(): MeshBuildingBlockRun? {
      return null
    }
  }

  /**
   * Strictly only required for Kubernetes only tests but we dont overcomplicate things and just
   * set it up to be always there. Does not hurt the non kubernetes runs.
   */
  @Component
  @Primary
  class TestRunTerminator : SingleShotRunner.RunTerminator {
    override fun exit(exitCode: Int) {
      log.info { "Exit with code: $exitCode" }
    }
  }

  @Component
  class NoOpPrivateKeyProvider : DecryptionService.PrivateKeyProvider {
    override val privateKey: String = "private-key"
  }
}
