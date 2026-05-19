package io.meshcloud.crypto.base

import io.meshcloud.crypto.Base64Encoding
import org.springframework.stereotype.Component
import java.nio.ByteBuffer
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 *
 * Provides symmetric encryption based on AES-128.
 * We use a random IV based approach here:
 * We generate a random IV, and then construct a cipherMessage from appending the following three components:
 * - size of IV in bytes
 * - IV
 * - actual cipherText, which is the encrypted plainText using AES-128 with given random IV and provided key
 * For decrypting we split the cipherMessage in the three parts and apply the AES-128 in decryption mode with
 * retrieved IV and key given.
 *
 * This implementation is based on
 * https://proandroiddev.com/security-best-practices-symmetric-encryption-with-aes-in-java-7616beaaade9
 *
 * It only uses standard java functionality, so we don't need further dependencies in our utils module.
 */
@Component
class MeshSymmetricCrypto {

  private val cipher: Cipher = Cipher.getInstance("AES/GCM/NoPadding")

  /**
   * Encrypts plainText with given key and returns the resulting [ByteArray].
   */
  @Throws(Exception::class)
  fun encryptToBytes(plainText: String, key: ByteArray): ByteArray {
    val iv = generateIv()
    val encryptedByte = encrypt(plainText, convertToSecretKey(key), iv)
    return buildCipherMessage(iv, encryptedByte)
  }

  /**
   * Decrypts the bytes with help of given key and returns the original plainText.
   */
  @Throws(Exception::class)
  fun decryptFromBytes(encryptedBytes: ByteBuffer, key: ByteArray): String {
    val iv = extractIv(encryptedBytes)
    val cipherText = readEncryptedText(encryptedBytes)

    return decrypt(iv, cipherText, key)
  }

  /**
   * Convenience encryption method that applies Base64 encoding on the encrypted
   * bytes for simpler follow-up processing.
   */
  @Throws(Exception::class)
  fun encrypt(plainText: String, key: String): String {
    val cipherMessage = encryptToBytes(plainText, key.toByteArray())
    return Base64Encoding.encodeBase64(cipherMessage)
  }

  /**
   * Convenience decryption method that applies Base64 decoding on given Base64-encoded
   * string before passing it to the decryption process.
   */
  @Throws(Exception::class)
  fun decrypt(encryptedText: String, key: String): String {
    val byteBuffer = Base64Encoding.readBase64Encoded(encryptedText)
    return decryptFromBytes(byteBuffer, key.toByteArray())
  }

  /**
   * Encrypts the plainText with given key and initialization vector.
   */
  private fun encrypt(plainText: String, secretKey: SecretKey, iv: ByteArray): ByteArray {
    val spec = GCMParameterSpec(128, iv)
    cipher.init(Cipher.ENCRYPT_MODE, secretKey, spec)
    val plainTextByte = plainText.toByteArray()

    return cipher.doFinal(plainTextByte)
  }

  /**
   * Generates a random initialization vector.
   */
  private fun generateIv(): ByteArray {
    val secureRandom = SecureRandom()
    val iv = ByteArray(12) //NEVER REUSE THIS IV WITH SAME KEY
    secureRandom.nextBytes(iv)

    return iv
  }

  /**
   * The cipher message from this symmetric encryption process is a
   * concatenation of three elements:
   * - the size (in bytes) of the initialization vector
   * - the initialization vector
   * - the actual encrypted plainText
   */
  private fun buildCipherMessage(iv: ByteArray, encryptedByte: ByteArray): ByteArray {
    val byteBuffer = ByteBuffer.allocate(4 + iv.size + encryptedByte.size)
    byteBuffer.putInt(iv.size)
    byteBuffer.put(iv)
    byteBuffer.put(encryptedByte)

    return byteBuffer.array()
  }

  /**
   * Extracts the initialization vector from a [ByteBuffer] by first
   * getting the size of the IV, and then read as many bytes from the buffer.
   */
  private fun extractIv(byteBuffer: ByteBuffer): ByteArray {
    val ivLength = byteBuffer.int
    if (ivLength < 12 || ivLength >= 16) { // check input parameter
      throw IllegalArgumentException("invalid iv length")
    }
    val iv = ByteArray(ivLength)
    byteBuffer.get(iv)

    return iv
  }

  /**
   * Reads the remaining bytes from a [ByteBuffer], where the initialization vector has
   * already been read from.
   */
  private fun readEncryptedText(byteBuffer: ByteBuffer): ByteArray {
    val cipherText = ByteArray(byteBuffer.remaining())
    byteBuffer.get(cipherText)

    return cipherText
  }

  /**
   * Decrypts the cipherText with given key and initialization vector.
   */
  private fun decrypt(iv: ByteArray, cipherText: ByteArray, plainKey: ByteArray): String {
    val secretKey = convertToSecretKey(plainKey)
    cipher.init(Cipher.DECRYPT_MODE, secretKey, GCMParameterSpec(128, iv))

    return String(cipher.doFinal(cipherText))
  }

  private fun convertToSecretKey(key: ByteArray): SecretKey {
    if (key.size != 16) {
      throw java.lang.IllegalArgumentException("Invalid Key! Key must be exactly 16 Bytes long (16 characters)!")
    }

    return SecretKeySpec(key, "AES")
  }
}
