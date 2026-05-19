package io.meshcloud.buildingblocks.runner.runclient

/**
 * This is useful for tests to overwrite the source of the environment variable so we
 * can control it.
 */
interface EnvironmentVariableProvider {
  fun fetchVariable(variableName: String): String?
}
