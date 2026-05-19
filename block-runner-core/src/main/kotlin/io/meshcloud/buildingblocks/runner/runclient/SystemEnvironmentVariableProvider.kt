package io.meshcloud.buildingblocks.runner.runclient

import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Component

@Component
@Profile("kubernetes")
class SystemEnvironmentVariableProvider : EnvironmentVariableProvider {
  override fun fetchVariable(variableName: String): String? {
    return System.getenv(variableName)
  }
}

