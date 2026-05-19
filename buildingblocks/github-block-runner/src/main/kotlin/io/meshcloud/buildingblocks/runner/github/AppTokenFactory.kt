package io.meshcloud.buildingblocks.runner.github

import com.auth0.jwt.JWT
import com.auth0.jwt.algorithms.Algorithm
import org.bouncycastle.asn1.ASN1Sequence
import org.bouncycastle.jce.provider.BouncyCastleProvider
import org.springframework.stereotype.Component
import java.security.KeyFactory
import java.security.Security
import java.security.interfaces.RSAPrivateKey
import java.security.spec.RSAPrivateKeySpec
import java.time.Instant
import java.util.Base64
import java.util.HashMap

@Component
class AppTokenFactory {

  /**
   * This fetches a token to authenticate against the
   * GitHub API for apps.
   */
  fun getAppAuthToken(
    appId: String,
    appPem: String
  ): String {
    val privateKey = getPrivateKeyFromPemPKCS1(appPem)

    // Prepare JWT payload
    val payload: MutableMap<String, Any> = HashMap()
    val now = Instant.now().epochSecond
    payload["iat"] = now - 10
    payload["exp"] = now + 300
    payload["iss"] = appId

    // Create JWT
    val algorithm: Algorithm = Algorithm.RSA256(null, privateKey)
    val token: String = JWT.create()
      .withPayload(payload)
      .sign(algorithm)

    return token
  }

  private fun getPrivateKeyFromPemPKCS1(appPem: String): RSAPrivateKey {
    // Remove the PEM headers and footers
    val privateKeyPEM = appPem
      .replace("-----BEGIN RSA PRIVATE KEY-----", "")
      .replace("-----END RSA PRIVATE KEY-----", "")
      .replace("\\s".toRegex(), "") // Remove newlines or spaces

    // Decode the Base64 encoded string
    val keyBytes = Base64.getDecoder().decode(privateKeyPEM)

    // Parse the ASN.1 sequence from the decoded bytes
    val asn1Sequence = ASN1Sequence.fromByteArray(keyBytes)

    // Convert the ASN.1 sequence to an RSAPrivateKey object
    val asn1RSAPrivateKey = org.bouncycastle.asn1.pkcs.RSAPrivateKey.getInstance(asn1Sequence)

    // Create RSAPrivateKeySpec from the ASN.1 RSA private key components
    val privateKeySpec = RSAPrivateKeySpec(asn1RSAPrivateKey.modulus, asn1RSAPrivateKey.privateExponent)

    // Generate the private key from the KeyFactory
    val keyFactory = KeyFactory.getInstance("RSA")

    return keyFactory.generatePrivate(privateKeySpec) as RSAPrivateKey
  }

  companion object {
    init {
      // Add the BouncyCastle security provider
      Security.addProvider(BouncyCastleProvider())
    }
  }
}
