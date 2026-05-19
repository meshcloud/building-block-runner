package io.meshcloud.crypto

import io.meshcloud.crypto.base.MeshSymmetricCrypto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class MeshSymmetricCryptoTest {

  private val sut = MeshSymmetricCrypto()

  @Test
  fun `encrypt does not return input`() {
    val text = "some text"
    val key = "my-random-key123"

    val encrypted = sut.encrypt(text, key)

    assertNotEquals(text, encrypted)
  }

  @Test(expected = IllegalArgumentException::class)
  fun `encrypt with invalid key length fails`() {
    val text = "some text"
    val key = "invalid"

    sut.encrypt(text, key)
  }

  @Test
  fun `encrypted text can be decrypted`() {
    val text = "some text"
    val key = "my-random-key123"

    val encrypted = sut.encrypt(text, key)
    val decrypted = sut.decrypt(encrypted, key)

    assertEquals(text, decrypted)
  }

}
