package io.meshcloud.buildingblocks.runner.runclient

import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.Test
import org.junit.jupiter.params.ParameterizedTest
import org.junit.jupiter.params.provider.ValueSource
import java.io.IOException
import java.time.Duration

class MeshStackRetryTest {

  private val clock = SteppingClock()
  private val sleeps = mutableListOf<Duration>()
  private val retry = MeshStackRetry(clock) { delay ->
    sleeps.add(delay)
    clock.advance(delay)
  }

  @Test
  fun `waits one second, then doubles the wait`() {
    val attempts = attemptsThat(rejectWith(503), rejectWith(503), succeed())

    val result = retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next)

    assertThat(result).isEqualTo("delivered")
    assertThat(attempts.count).isEqualTo(3)
    assertThat(sleeps).containsExactly(Duration.ofSeconds(1), Duration.ofSeconds(2))
  }

  @ParameterizedTest
  @ValueSource(ints = [423, 502, 503, 504])
  fun `retries a status that passes once meshStack is back`(statusCode: Int) {
    val attempts = attemptsThat(rejectWith(statusCode), rejectWith(statusCode), succeed())

    retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next)

    assertThat(attempts.count).isEqualTo(3)
  }

  @ParameterizedTest
  @ValueSource(ints = [400, 403, 409, 500])
  fun `does not retry an internal server error or a client error`(statusCode: Int) {
    val attempts = attemptsThat(rejectWith(statusCode))

    assertThatThrownBy { retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next) }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessageContaining("$statusCode")
      .hasMessageNotContaining("earlier attempt")
    assertThat(attempts.count).isEqualTo(1)
  }

  @ParameterizedTest
  @ValueSource(ints = [401, 500])
  fun `says an earlier attempt may have delivered when meshStack rejects a retry`(statusCode: Int) {
    val attempts = attemptsThat(rejectWith(504), rejectWith(statusCode))

    assertThatThrownBy { retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next) }
      .isInstanceOf(MeshStackRequestOutcomeUnknownException::class.java)
      .hasMessageContaining("An earlier attempt may have delivered the status update for run run-1")
      .hasMessageContaining("$statusCode")
    assertThat(attempts.count).isEqualTo(2)
  }

  @Test
  fun `retries when meshStack is unreachable`() {
    val attempts = attemptsThat({ throw IOException("Connection refused") }, succeed())

    retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next)

    assertThat(attempts.count).isEqualTo(2)
  }

  @Test
  fun `does not retry an error that is not about reaching meshStack`() {
    val attempts = attemptsThat({ throw IllegalArgumentException("broken payload") })

    assertThatThrownBy { retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next) }
      .isInstanceOf(IllegalArgumentException::class.java)
    assertThat(attempts.count).isEqualTo(1)
  }

  @Test
  fun `gives up after ten minutes, waiting at most 30 seconds between attempts`() {
    val start = clock.instant()
    val attempts = attemptsThat(rejectWith(503))

    assertThatThrownBy { retry.call("run-1", "status update", outcomeUnknownAfterRetry = true, attempts::next) }
      .isInstanceOf(MeshStackRequestOutcomeUnknownException::class.java)
      .hasMessageContaining("run-1")
      .hasCauseInstanceOf(MeshStackRejectedRequestException::class.java)

    val waited = Duration.between(start, clock.instant())
    assertThat(waited).isLessThanOrEqualTo(Duration.ofMinutes(10))
    assertThat(waited).isGreaterThan(Duration.ofMinutes(10).minusSeconds(30))
    assertThat(sleeps).allSatisfy { assertThat(it).isLessThanOrEqualTo(Duration.ofSeconds(30)) }
    assertThat(sleeps.last()).isEqualTo(Duration.ofSeconds(30))
  }

  @Test
  fun `throws the last failure as it is when giving up on a request that cannot have delivered`() {
    val attempts = attemptsThat(rejectWith(503))

    assertThatThrownBy { retry.call("run-1", "source registration", outcomeUnknownAfterRetry = false, attempts::next) }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessageContaining("503")
  }

  @Test
  fun `throws the rejection as it is when meshStack rejects a retry of a request that cannot have delivered`() {
    val attempts = attemptsThat(rejectWith(504), rejectWith(401))

    assertThatThrownBy { retry.call("run-1", "source registration", outcomeUnknownAfterRetry = false, attempts::next) }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessageContaining("401")
    assertThat(attempts.count).isEqualTo(2)
  }

  private fun rejectWith(statusCode: Int): () -> String = {
    throw MeshStackRejectedRequestException(statusCode, "meshStack answered HTTP $statusCode")
  }

  private fun succeed(): () -> String = { "delivered" }

  private fun attemptsThat(vararg outcomes: () -> String) = Attempts(outcomes.toList())

  private class Attempts(private val outcomes: List<() -> String>) {
    var count = 0
      private set

    fun next(): String {
      val outcome = outcomes[minOf(count, outcomes.size - 1)]
      count++
      return outcome()
    }
  }
}
