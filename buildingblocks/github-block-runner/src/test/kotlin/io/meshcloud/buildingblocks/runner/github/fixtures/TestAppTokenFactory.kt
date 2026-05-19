package io.meshcloud.buildingblocks.runner.github.fixtures

import io.meshcloud.buildingblocks.runner.github.AppTokenFactory

/**
 * Test AppTokenFactory that returns a fixed token without requiring actual key material.
 * This eliminates the need for valid GitHub App credentials in tests.
 */
class TestAppTokenFactory : AppTokenFactory() {
  override fun getAppAuthToken(appId: String, appPem: String): String {
    return "test-app-auth-token"
  }
}
