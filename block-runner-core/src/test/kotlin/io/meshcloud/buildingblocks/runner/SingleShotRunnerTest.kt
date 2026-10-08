package io.meshcloud.buildingblocks.runner

import io.meshcloud.buildingblocks.runner.runclient.MeshStackRequestOutcomeUnknownException
import io.mockk.every
import io.mockk.just
import io.mockk.mockk
import io.mockk.runs
import io.mockk.verify
import org.junit.jupiter.api.Test
import java.io.IOException

class SingleShotRunnerTest {

  private val blockRunnerService = mockk<BlockRunnerService>()
  private val terminator = mockk<SingleShotRunner.RunTerminator> {
    every { exit(any()) } just runs
  }
  private val runner = SingleShotRunner(blockRunnerService, terminator)

  @Test
  fun `exits 0 when the run was processed`() {
    every { blockRunnerService.processBlock() } returns null

    runner.run()

    verify(exactly = 1) { terminator.exit(0) }
  }

  @Test
  fun `exits 0 when meshStack may not have received the run's status, so Kubernetes does not run it again`() {
    every { blockRunnerService.processBlock() } throws
      MeshStackRequestOutcomeUnknownException("meshStack stayed unavailable", IOException("connection refused"))

    runner.run()

    verify(exactly = 1) { terminator.exit(0) }
  }

  @Test
  fun `exits 1 when the run failed for another reason`() {
    every { blockRunnerService.processBlock() } throws IllegalStateException("broken")

    runner.run()

    verify(exactly = 1) { terminator.exit(1) }
  }
}
