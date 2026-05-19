package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.*
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Profile

@Configuration
class BlockRunnerServiceConfiguration(
  private val blockRunClientFetcher: BlockRunClientFetcher,
) {

  @Bean
  @Profile("!kubernetes")
  fun blockRunnerService(
    gitlabClientFactory: GitLabClientFactory,
    decryptionService: DecryptionService,
  ): BlockRunnerService {
    val service = GitLabBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      gitlabClientFactory = gitlabClientFactory,
      decryptionService = decryptionService
    )

    return ImmediateRetryDecorator(service)
  }

  @Bean
  @Profile("kubernetes")
  fun kubernetesBlockRunnerService(
    gitlabClientFactory: GitLabClientFactory,
    decryptionService: DecryptionService,
  ): BlockRunnerService {
    return GitLabBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcher,
      gitlabClientFactory = gitlabClientFactory,
      decryptionService = decryptionService
    )
  }
}
