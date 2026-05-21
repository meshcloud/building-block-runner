package io.meshcloud.buildingblocks.runner.github

import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.context.properties.ConfigurationPropertiesScan
import org.springframework.boot.runApplication


@ConfigurationPropertiesScan(basePackages = [
  "io.meshcloud.buildingblocks.runner",
])
@SpringBootApplication(scanBasePackages = [
  "io.meshcloud.buildingblocks.runner",
])
class GitHubBlockRunnerApplication

fun main(args: Array<String>) {
  runApplication<GitHubBlockRunnerApplication>(*args)
}
