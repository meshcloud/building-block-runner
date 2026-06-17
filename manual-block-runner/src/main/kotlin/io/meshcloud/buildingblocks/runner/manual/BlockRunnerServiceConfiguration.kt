package io.meshcloud.buildingblocks.runner.manual

import io.meshcloud.buildingblocks.runner.*
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Profile

@Configuration
class BlockRunnerServiceConfiguration(
  private val blockRunClientFetcher: BlockRunClientFetcher,
  private val manualRunnerConfig: ManualRunnerConfig,
) {

  @Bean
  @Profile("!kubernetes")
  fun blockRunnerService(): BlockRunnerService {
    return ImmediateRetryDecorator(getRunnerService())
  }

  /**
   * Kubernetes based services should not retry.
   */
  @Bean
  @Profile("kubernetes")
  fun kubernetesBlockRunnerService(): BlockRunnerService {
    return getRunnerService()
  }

  private fun getRunnerService(): BlockRunnerService {
    val blockRunnerService = if (manualRunnerConfig.debugMode) {
      DebugBlockRunnerService(blockRunClientFetcher)
    } else {
      NoOpBlockRunnerService(blockRunClientFetcher)
    }

    return blockRunnerService
  }
}
