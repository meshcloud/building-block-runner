package io.meshcloud.buildingblocks.runner

import io.github.oshai.kotlinlogging.KotlinLogging
import org.springframework.boot.CommandLineRunner
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component
import kotlin.system.exitProcess

private val log = KotlinLogging.logger { }

/**
 * This is especially useful for the kubernetes environment. It will just perform a single execution
 * and then terminate itself after the single building block was executed.
 */
@Component
@Profile("kubernetes")
class SingleShotRunner(
  private val blockRunnerService: BlockRunnerService,
  private val terminator: RunTerminator
) : CommandLineRunner {

  /**
   * Pluggable terminator so it can be replaced inside tests because Spring does not like
   * If exitProcess() is called inside tests.
   */
  interface RunTerminator {
    fun exit(exitCode: Int)
  }

  @Component
  @Profile("kubernetes")
  class SystemRunTerminator : RunTerminator {
    override fun exit(exitCode: Int) {
      exitProcess(exitCode)
    }
  }

  override fun run(vararg args: String?) {
    try {
      blockRunnerService.processBlock()

      // Exit with success code
      terminator.exit(0)
    } catch (e: Exception) {
      log.error(e) { "Job failed: ${e.message}" }
      // Exit with error code
      terminator.exit(1)
    }
  }
}
