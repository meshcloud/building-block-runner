package io.meshcloud.crypto

import org.junit.Assert.assertNotNull
import org.junit.Test
import java.security.spec.InvalidKeySpecException

class KeyLoaderTest {

  private val x509CertString = object {}.javaClass.getResource("/crypto/test.pem")!!.readText()

  private val pemPublicKeyString = object {}.javaClass.getResource("/crypto/test-public.pem")!!.readText()

  @Test
  fun loadPublicKey_parsesX509CertificateString() {
    val result = KeyLoader.loadPublicKey(x509CertString)

    assertNotNull(result)
  }

  @Test
  fun loadPublicKey_parsesPemPublicKeyString() {
    val result = KeyLoader.loadPublicKey(pemPublicKeyString)

    assertNotNull(result)
  }

  @Test(expected = InvalidKeySpecException::class)
  fun loadPublicKey_throwsExceptionForInvalidKey() {
    KeyLoader.loadPublicKey("this-is-not-a-valid-key")
  }
}
