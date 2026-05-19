package io.meshcloud.buildingblocks.runner

import io.meshcloud.buildingblocks.runner.logging.RequestLoggingUtility
import org.springframework.context.annotation.Profile
import org.springframework.scheduling.annotation.Scheduled
import org.springframework.stereotype.Component

@Component
@Profile("!kubernetes")
class BlockRunRequestScheduler(
  private val blockRunnerService: BlockRunnerService
) {

  @Scheduled(fixedRate = 10000)
  fun requestPendingBlocks() {
    RequestLoggingUtility.withLoggingRequestId("sched") {
      blockRunnerService.processBlock()
    }
  }
}
