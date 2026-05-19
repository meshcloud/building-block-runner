package io.meshcloud.crypto.base

import io.meshcloud.crypto.Base64Encoding
import org.springframework.stereotype.Component
import java.nio.ByteBuffer
import java.security.Key
import java.security.PrivateKey
import java.security.PublicKey
import java.security.SecureRandom
import java.security.interfaces.RSAPrivateKey
import java.security.interfaces.RSAPublicKey
import javax.crypto.Cipher

/**
 * SECURITY_MEASURE: MESH.OP.CRYPT.030
 *
 * This helper allows to encrypt a sensitive value using a [PublicKey] so only the holder of
 * the [PrivateKey] is able to decrypt the encrypted secret.
 *
 * Asymmetric encryption however has some limits regarding the size of the plaintext data.
 * (e.g. for RSA the maximum length is at max modulus-size / 8, for RSA-4096 that would be 64 byte)
 * A common approach therefore is to use the asymmetric encryption only for encrypting a random
 * symmetric key, e.g. an AES key, and then use this symmetric key to encrypt the actual plaintext data.
 * The encrypted symmetric key is prepended before the cipherText.
 * For decryption, we will use the asymmetric private key to decrypt the encrypted symmetric key
 * and then use the symmetric key to obtain back the encrypted plaintext.
 *
 * Example:
 * Alice:
 * - generate random symmetric AES-256 key: A
 * - use A to encrypt the secret plaintext value P, so that a ciphertext C exists
 * - use the public key PUK to encrypt A, so that the encrypted random AES key E exists
 * - now send the value E.C to Bob (the '.' represents a concatenation operation)
 * Bob:
 * - receive E.C
 * - split the received value into E and C
 * - use the private key PVK to decrypt E so that the symmetric key A is obtained
 * - use A to decrypt C, so that the original plaintext value P is obtained
 *
 * We don't need to re-invent the wheel here, so we use the [MeshSymmetricCrypto]
 * for the symmetric encryption part.
 *
 * Note: [Cipher] is not thread safe, that's why new instances are used everytime.
 */
@Component
class MeshCertBasedCrypto(
  private val symmetricCrypto: MeshSymmetricCrypto
) {

  /**
   * SECURITY_MEASURE: MESH.OP.CRYPT.030
   */
  private val ASYMMETRIC_CIPHER_TRANSFORMATION = "RSA/ECB/OAEPWithSHA1AndMGF1Padding"

  /**
   * Applies encryption to given [ByteArray] with the scheme described at this class's header.
   * Also applies Base64 encoding on the resulting cipherText in byte format for easier follow-up
   * processing.
   */
  fun applyEncryption(publicKey: RSAPublicKey, data: String): String {
    // obtain cipher for asymmetric encryption
    val cipher = obtainAsymmetricCipher(Cipher.ENCRYPT_MODE, publicKey)

    // create random symmetric key and encrypt plaintext with it
    val randomSymmetricKey = generateRandomSymmetricKey()
    val symmetricEncryptionResult = symmetricCrypto.encryptToBytes(data, randomSymmetricKey)

    // use asymmetric encryption to encrypt used symmetric key
    val encryptedRandomKey = cipher.doFinal(randomSymmetricKey)

    // now simply append the ciphertext after the encrypted random symmetric key
    return Base64Encoding.encodeBase64(encryptedRandomKey + symmetricEncryptionResult)
  }

  /**
   * Applies Base64 decoding on a given string and decrypts it with the scheme described at this class's header.
   */
  fun applyDecryption(privateKey: RSAPrivateKey, encrypted: String): String {
    if (encrypted.isEmpty()) {
      return ""
    }

    val encryptedBytes = Base64Encoding.readBase64Encoded(encrypted).array()

    // find out length of encrypted random symmetric key
    val encryptedRandomKeyLength = privateKey.modulus.bitLength() / 8

    // first n byte are the encrypted random symmetric key, the rest is the ciphertext
    val encryptedRandomKey = encryptedBytes.copyOfRange(0, encryptedRandomKeyLength)

    val symmetricEncryptionResultLength = encryptedBytes.size - encryptedRandomKeyLength
    val symmetricEncryptionResult = encryptedBytes.copyOfRange(encryptedRandomKeyLength, encryptedRandomKeyLength + symmetricEncryptionResultLength)

    // obtain the random symmetric key by decrypting it
    val cipher = obtainAsymmetricCipher(Cipher.DECRYPT_MODE, privateKey)
    val symmetricKey = cipher.doFinal(encryptedRandomKey)

    return symmetricCrypto.decryptFromBytes(ByteBuffer.wrap(symmetricEncryptionResult), symmetricKey)
  }

  /**
   * Helper to obtain the asymmetric cipher used.
   */
  private fun obtainAsymmetricCipher(opMode: Int, key: Key): Cipher {
    val cipher = Cipher.getInstance(ASYMMETRIC_CIPHER_TRANSFORMATION)
    cipher.init(opMode, key)

    return cipher
  }

  /**
   * Will generate 16 random bytes that can be utilized as
   * AES-128 key e.g. in [MeshSymmetricCrypto].
   */
  private fun generateRandomSymmetricKey(): ByteArray {
    val randomBytes = ByteArray(16)
    val random = SecureRandom()
    random.nextBytes(randomBytes)

    return randomBytes
  }
}
