package io.meshcloud.crypto

import java.nio.ByteBuffer
import java.util.*

/**
 * Provides functionality to transform between a [ByteArray]
 * and a String representing it in Base64 encoded format.
 */
object Base64Encoding {

  fun encodeBase64(bytes: ByteArray): String {
    return Base64.getEncoder().encodeToString(bytes)
  }

  fun readBase64Encoded(text: String): ByteBuffer {
    return ByteBuffer.wrap(Base64.getDecoder().decode(text))
  }
}
