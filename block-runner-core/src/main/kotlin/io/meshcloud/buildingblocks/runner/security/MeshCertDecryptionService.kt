package io.meshcloud.buildingblocks.runner.security

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Service
import java.io.BufferedReader
import java.io.InputStream
import java.io.InputStreamReader
import java.nio.ByteBuffer
import java.security.KeyFactory
import java.security.interfaces.RSAPrivateKey
import java.security.spec.PKCS8EncodedKeySpec
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

private val log = KotlinLogging.logger { }

/**
 * Decryption service for non-kubernetes runners.
 */
@Service
@Profile("!kubernetes")
class MeshCertDecryptionService(
  private val cryptoConfig: DecryptionService.PrivateKeyProvider,
) : DecryptionService {

  private val asymmetricCipherTransformation = "RSA/ECB/OAEPWithSHA1AndMGF1Padding"

  override fun decrypt(secret: String): String {
    if (secret.isEmpty()) {
      return ""
    }

    val decryptionKey = loadPrivateKey(cryptoConfig.privateKey.byteInputStream(Charsets.UTF_8))
    val encryptedBytes = Base64.getDecoder().decode(secret)
    val encryptedRandomKeyLength = decryptionKey.modulus.bitLength() / 8

    val encryptedRandomKey = encryptedBytes.copyOfRange(0, encryptedRandomKeyLength)
    val symmetricEncryptionResult = encryptedBytes.copyOfRange(encryptedRandomKeyLength, encryptedBytes.size)

    val asymmetricCipher = Cipher.getInstance(asymmetricCipherTransformation)
    asymmetricCipher.init(Cipher.DECRYPT_MODE, decryptionKey)
    val symmetricKey = asymmetricCipher.doFinal(encryptedRandomKey)

    return decryptSymmetricPayload(ByteBuffer.wrap(symmetricEncryptionResult), symmetricKey)
  }

  /**
   * For the dispatched run we need to decrypt all the encrypted input values and provide them in plaintext.
   *
   * Might make sense to have a separate service for this task but for simplicity reasons it was included in here.
   */
  override fun decryptBlockRunInputs(run: ProcessableBlockRun): ProcessableBlockRun {
    val readableInputs = run.meshObject.spec.buildingBlock.spec.inputs.map { input ->
      if (input.isSensitive) {
        when (input.type) {
          MeshBuildingBlockIOType.STRING -> {
            input.copy(value = decrypt(input.value.toString()))
          }

          MeshBuildingBlockIOType.CODE -> {
            input.copy(value = decrypt(input.value.toString()))
          }

          MeshBuildingBlockIOType.FILE -> {
            input.copy(value = decrypt(input.value.toString()))
          }

          else -> {
            log.error { "Cannot decrypt a sensitive input that is neither a 'STRING', 'CODE', or 'FILE' type. Will leave it as is." }
            input
          }
        }
      } else {
        input
      }
    }

    return ProcessableBlockRun(
      links = run.links,
      meshObject = run.meshObject.copy(
        spec = run.meshObject.spec.copy(
          buildingBlock = MeshBuildingBlockRun.MeshBuildingBlockRunSpec.BuildingBlock(
            uuid = run.meshObject.spec.buildingBlock.uuid,
            spec = run.meshObject.spec.buildingBlock.spec.copy(
              inputs = readableInputs,
            ),
          ),
        ),
      ),
    )
  }

  private fun decryptSymmetricPayload(encryptedBytes: ByteBuffer, key: ByteArray): String {
    val ivLength = encryptedBytes.int
    if (ivLength < 12 || ivLength >= 16) {
      throw IllegalArgumentException("invalid iv length")
    }

    val iv = ByteArray(ivLength)
    encryptedBytes.get(iv)

    val cipherText = ByteArray(encryptedBytes.remaining())
    encryptedBytes.get(cipherText)

    if (key.size != 16) {
      throw IllegalArgumentException("Invalid Key! Key must be exactly 16 Bytes long (16 characters)!")
    }

    val cipher = Cipher.getInstance("AES/GCM/NoPadding")
    val secretKey = SecretKeySpec(key, "AES")
    cipher.init(Cipher.DECRYPT_MODE, secretKey, GCMParameterSpec(128, iv))

    return String(cipher.doFinal(cipherText))
  }

  private fun loadPrivateKey(inputStream: InputStream): RSAPrivateKey {
    val bufferedReader = BufferedReader(InputStreamReader(inputStream))

    val stringBuilder = StringBuilder(bufferedReader.readLine())
    bufferedReader.lines().forEach {
      stringBuilder.append("\n" + it)
    }

    val privateKeyContent = stringBuilder.toString()
      .replace("-----BEGIN PRIVATE KEY-----", "")
      .replace("-----END PRIVATE KEY-----", "")
      .replace("\n", "")

    val decoded = Base64.getMimeDecoder().decode(privateKeyContent)
    val keyFactory = KeyFactory.getInstance("RSA")

    return keyFactory.generatePrivate(PKCS8EncodedKeySpec(decoded)) as RSAPrivateKey
  }
}
