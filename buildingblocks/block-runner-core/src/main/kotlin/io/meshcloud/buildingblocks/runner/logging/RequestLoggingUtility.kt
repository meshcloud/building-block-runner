package io.meshcloud.buildingblocks.runner.logging

import mu.withLoggingContext
import org.hashids.Hashids
import java.time.LocalDateTime

/**
 * Utility for adding request IDs to logs for better traceability in block runner operations.
 *
 * Instead of depending on a shared core/logging module, we inline this functionality here
 * to keep the runners lean. This is only used once in the scheduler, so the duplication
 * is minimal and acceptable.
 */
internal object RequestLoggingUtility {
  const val REQUEST_ID_KEY = "requestId"
  internal val hash = Hashids("meshcloud-salt")

  /**
   * @param prefix The prefix is limited to 6 characters! If you provide more, it will be cut off!
   *   The prefix is added in front of the requestId to make it easily identifiable what kind of request
   *   the log statement belongs to (e.g. rest call, rabbit listener invocation, scheduled job, etc).
   */
  inline fun <T> withLoggingRequestId(prefix: String, body: () -> T): T {
    val cutOffPrefix = prefix.take(6)
    return withLoggingContext(REQUEST_ID_KEY to "$cutOffPrefix-${generateId()}") {
      body()
    }
  }

  private fun generateId(): String {
    // month, day and millisecond of day
    val now = LocalDateTime.now()
    return hash.encode(now.monthValue.toLong(), now.dayOfMonth.toLong(), now.toLocalTime().toNanoOfDay())
  }
}
