package io.meshcloud.buildingblocks.runner.github.fixtures

import io.meshcloud.buildingblocks.runner.security.DecryptionService

/**
 * Test implementation of PrivateKeyProvider that returns a fixed test key.
 */
class TestPrivateKeyProvider : DecryptionService.PrivateKeyProvider {
  override val privateKey: String = "test-private-key"
}
