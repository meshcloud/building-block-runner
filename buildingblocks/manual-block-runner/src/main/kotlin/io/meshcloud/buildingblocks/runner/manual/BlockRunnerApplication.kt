package io.meshcloud.buildingblocks.runner.manual

import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.context.properties.ConfigurationPropertiesScan
import org.springframework.boot.runApplication

@ConfigurationPropertiesScan(basePackages = [
  "io.meshcloud.buildingblocks.runner",
])
@SpringBootApplication(scanBasePackages = [
  "io.meshcloud.buildingblocks.runner"
])
class BlockRunnerApplication

fun main(args: Array<String>) {
  runApplication<BlockRunnerApplication>(*args)
}
