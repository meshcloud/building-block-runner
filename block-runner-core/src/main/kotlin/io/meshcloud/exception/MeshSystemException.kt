package io.meshcloud.exception

import org.hashids.Hashids
import java.time.LocalDateTime

/**
 * This is the very basic type of MeshExceptions. It only contains a systemMessage which is not meant
 * to be send to a end-user. It usually contains internal information for debugging purposes for operators.
 */
open class MeshSystemException(
  val systemMessage: String,
  cause: Throwable? = null,
  /**
   * A user-visible error idea that enables correlation of errors into our logs.
   */
  val errorId: String = generateId()
) : RuntimeException(systemMessage, cause) {

  companion object {

    private val hash = Hashids("meshcloud-salt")

    fun generateId(): String {
      // month, day and millisecond of day
      val now = LocalDateTime.now()
      return hash.encode(now.monthValue.toLong(), now.dayOfMonth.toLong(), now.toLocalTime().toNanoOfDay() / 1000000)
    }
  }
}
