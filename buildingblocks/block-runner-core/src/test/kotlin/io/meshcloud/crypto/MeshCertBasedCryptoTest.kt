package io.meshcloud.crypto

import io.meshcloud.crypto.base.MeshCertBasedCrypto
import io.meshcloud.crypto.base.MeshSymmetricCrypto
import org.junit.Assert
import org.junit.Before
import org.junit.Test
import java.security.interfaces.RSAPrivateKey
import java.security.interfaces.RSAPublicKey

class MeshCertBasedCryptoTest {

  lateinit var publicKey: RSAPublicKey
  lateinit var privateKey: RSAPrivateKey

  private lateinit var sut: MeshCertBasedCrypto

  @Before
  fun init() {
    publicKey = KeyLoader.loadPublicKey(object {}.javaClass.getResource("/crypto/test.pem")?.openStream()!!)
    privateKey = KeyLoader.loadPrivateKey(object {}.javaClass.getResource("/crypto/test.key")?.openStream()!!)

    sut = MeshCertBasedCrypto(MeshSymmetricCrypto())
  }

  @Test
  fun stringEncryptionWithVariousPlaintextLengths() {
    randomStringList().forEach {
      val encrypted = sut.applyEncryption(publicKey, it)
      val result = sut.applyDecryption(privateKey, encrypted)
      Assert.assertEquals(it, result)
    }
  }

  private fun randomStringList(): List<String> {
    return listOf(0, 1, 10, 100, 1000, 10_000)
      .map { randomString(it) }
      .toList()
  }

  private fun randomString(length: Int): String {
    return (1..length).map { charPool.random() }.joinToString("")
  }

  companion object {
    private val charPool: List<Char> =
      ('a'..'z') +
        ('A'..'Z') +
        ('0'..'9') +
        listOf(' ', '`', '~', '!', '@', '#', '$', '%', '^', '&', '*', '(', ')', '_', '-', '+', '=', '{', '[', '}', '}', '|', '\\', ':', ';', '"', '\'', '<', ',', '>', '.', '?', '/')
  }
}
