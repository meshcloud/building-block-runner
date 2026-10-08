package io.meshcloud.buildingblocks.runner.runclient

import io.github.oshai.kotlinlogging.KotlinLogging
import java.io.IOException
import java.time.Clock
import java.time.Duration

private val log = KotlinLogging.logger { }

class MeshStackRetry(
  private val clock: Clock = Clock.systemUTC(),
  private val sleeper: (Duration) -> Unit = { Thread.sleep(it) },
) {

  /**
   * With [outcomeUnknownAfterRetry], a failure after the first attempt raises [MeshStackRequestOutcomeUnknownException],
   * because the runner cannot tell whether an earlier attempt reached meshStack.
   */
  fun <T> call(runId: String, request: String, outcomeUnknownAfterRetry: Boolean, attempt: () -> T): T {
    val deadline = clock.instant().plus(GIVE_UP_AFTER)
    var delay = INITIAL_DELAY
    var retried = false

    while (true) {
      try {
        return attempt()
      } catch (ex: Exception) {
        val givingUp = Duration.between(clock.instant(), deadline) <= delay
        if (!isTransient(ex) || givingUp) {
          throw if (outcomeUnknownAfterRetry && retried) {
            MeshStackRequestOutcomeUnknownException(
              "An earlier attempt may have delivered the $request for run $runId, but the last one failed: ${ex.message}",
              ex,
            )
          } else {
            ex
          }
        }

        log.warn { "The $request for run $runId failed, retrying in $delay: ${ex.message}" }
        sleeper(delay)
        retried = true
        delay = minOf(delay.multipliedBy(2), MAX_DELAY)
      }
    }
  }

  // 423 is meshStack failing to lock the run row because another change to it is in progress.
  // Leaves out 500: it is unlikely to pass on a retry, and a retry holds one of the few runner slots for ten minutes,
  // so a run that always fails with 500 could keep the runner from starting other runs.
  private fun isTransient(ex: Exception): Boolean = when (ex) {
    is MeshStackRejectedRequestException -> ex.statusCode in TRANSIENT_STATUS_CODES
    is IOException -> true
    else -> false
  }

  companion object {
    private val TRANSIENT_STATUS_CODES = setOf(423, 502, 503, 504)

    private val INITIAL_DELAY = Duration.ofSeconds(1)
    private val MAX_DELAY = Duration.ofSeconds(30)

    // meshStack answers 503 for about two minutes while a deployment replaces its only pod.
    private val GIVE_UP_AFTER = Duration.ofMinutes(10)
  }
}
