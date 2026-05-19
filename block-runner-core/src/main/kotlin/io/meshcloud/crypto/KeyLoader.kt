package io.meshcloud.crypto

import java.io.BufferedReader
import java.io.InputStream
import java.io.InputStreamReader
import java.security.KeyFactory
import java.security.cert.CertificateFactory
import java.security.interfaces.RSAPrivateKey
import java.security.interfaces.RSAPublicKey
import java.security.spec.PKCS8EncodedKeySpec
import java.util.*

object KeyLoader {

  /**
   * The [InputStream] is expected to provide the contents of
   * an RSA private key file in .pem format.
   */
  fun loadPrivateKey(inputStream: InputStream): RSAPrivateKey {
    val bufferedReader = BufferedReader(InputStreamReader(inputStream))

    val stringBuilder = StringBuilder(bufferedReader.readLine())
    bufferedReader.lines().forEach {
      stringBuilder.append("\n" + it)
    }

    val privKeyString = stringBuilder.toString()
      .replace("-----BEGIN PRIVATE KEY-----", "")
      .replace("-----END PRIVATE KEY-----", "")
      .replace("\n", "")

    val decoded = Base64.getMimeDecoder().decode(privKeyString)
    val pkcs8EncodedKeySpec = PKCS8EncodedKeySpec(decoded)
    val keyFactory = KeyFactory.getInstance("RSA")

    return keyFactory.generatePrivate(pkcs8EncodedKeySpec) as RSAPrivateKey
  }

  /**
   * The [InputStream] is expected to provide the contents of
   * a certificate in X.509 format that holds an RSA public key.
   */
  fun loadPublicKey(inputStream: InputStream): RSAPublicKey {
    val cert = CertificateFactory.getInstance("X.509").generateCertificate(inputStream)

    return cert.publicKey as RSAPublicKey
  }

  /**
   * Loads a public key from a string. First tries to parse as an X.509 certificate,
   * and if that fails, tries to parse as a PEM-encoded RSA public key
   * (PKCS#8 SubjectPublicKeyInfo format with "-----BEGIN PUBLIC KEY-----" headers).
   */
  fun loadPublicKey(keyOrCertString: String): RSAPublicKey {
    return try {
      loadPublicKey(keyOrCertString.byteInputStream(Charsets.UTF_8))
    } catch (_: Exception) {
      loadPublicKeyFromPem(keyOrCertString.byteInputStream(Charsets.UTF_8))
    }
  }

  /**
   * Loads a public key from a PEM-encoded string in PKCS#8 SubjectPublicKeyInfo format.
   * The [InputStream] is expected to provide the contents of an RSA public key file
   * in PEM format (with "-----BEGIN PUBLIC KEY-----" and "-----END PUBLIC KEY-----" headers).
   */
  private fun loadPublicKeyFromPem(inputStream: InputStream): RSAPublicKey {
    val bufferedReader = BufferedReader(InputStreamReader(inputStream))

    val stringBuilder = StringBuilder(bufferedReader.readLine())
    bufferedReader.lines().forEach {
      stringBuilder.append("\n" + it)
    }

    val pubKeyString = stringBuilder.toString()
      .replace("-----BEGIN PUBLIC KEY-----", "")
      .replace("-----END PUBLIC KEY-----", "")
      .replace("\n", "")

    val decoded = Base64.getMimeDecoder().decode(pubKeyString)
    val keyFactory = KeyFactory.getInstance("RSA")

    return keyFactory.generatePublic(java.security.spec.X509EncodedKeySpec(decoded)) as RSAPublicKey
  }
}
