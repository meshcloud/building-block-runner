package io.meshcloud.buildingblocks.runner.github.fixtures

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.security.DecryptionService

/**
 * Test DecryptionService that simulates decryption by removing the "encrypted:" prefix.
 * This allows tests to verify sensitive data handling without requiring actual encryption keys.
 */
class TestDecryptionService : DecryptionService {
  override fun decrypt(secret: String): String {
    // Simulate decryption by removing the "encrypted:" prefix if present
    return if (secret.startsWith("encrypted:")) {
      secret.removePrefix("encrypted:")
    } else {
      secret
    }
  }

  override fun decryptBlockRunInputs(run: ProcessableBlockRun): ProcessableBlockRun {
    val decryptedInputs = run.meshObject.spec.buildingBlock.spec.inputs.map { input ->
      if (input.isSensitive && input.value.toString().startsWith("encrypted:")) {
        input.copy(value = input.value.toString().removePrefix("encrypted:"))
      } else {
        input
      }
    }

    return run.copy(
      meshObject = run.meshObject.copy(
        spec = run.meshObject.spec.copy(
          buildingBlock = run.meshObject.spec.buildingBlock.copy(
            spec = run.meshObject.spec.buildingBlock.spec.copy(
              inputs = decryptedInputs,
            ),
          ),
        ),
      ),
    )
  }
}
