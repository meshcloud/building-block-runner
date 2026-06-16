package io.meshcloud.buildingblocks.runner.github

import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Profile
import org.springframework.scheduling.annotation.EnableScheduling

/**
 * Scheduling is disabled when test or kubernetes profile is active so it does not interfere and slow down tests.
 */
@Profile("!test & !kubernetes")
@Configuration
@EnableScheduling
class GitHubBlockRunnerSchedulingConfiguration
