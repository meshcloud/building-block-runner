package io.meshcloud.buildingblocks.runner.security

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.crypto.KeyLoader
import io.meshcloud.crypto.base.MeshCertBasedCrypto
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import mu.KotlinLogging
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Service
import java.nio.charset.Charset

private val log = KotlinLogging.logger { }

/**
 * This bean only loads if you component scan the crypto to make it more convenient to not
 * always use decryption (e.g. the manual runner or kubernetes mode where run-controller handles decryption).
 */
@Service
@Profile("!kubernetes")
@ConditionalOnBean(MeshCertBasedCrypto::class)
class MeshCertDecryptionService(
  private val meshCertBasedCrypto: MeshCertBasedCrypto,
  private val cryptoConfig: DecryptionService.PrivateKeyProvider,
) : DecryptionService {

  override fun decrypt(secret: String): String {
    val decryptionKey = KeyLoader.loadPrivateKey(
      cryptoConfig.privateKey.byteInputStream(Charset.defaultCharset())
    )
    return meshCertBasedCrypto.applyDecryption(decryptionKey, secret)
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
              inputs = readableInputs
            )
          )
        )
      )
    )
  }
}
