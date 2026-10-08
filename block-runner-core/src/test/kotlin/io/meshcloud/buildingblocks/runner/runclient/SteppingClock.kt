package io.meshcloud.buildingblocks.runner.runclient

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset

/** A clock that moves only when a test's sleeper advances it, so retries over ten minutes take no time. */
class SteppingClock : Clock() {
  private var now = Instant.parse("2026-10-08T12:00:00Z")

  fun advance(duration: Duration) {
    now = now.plus(duration)
  }

  override fun instant(): Instant = now

  override fun getZone(): ZoneId = ZoneOffset.UTC

  override fun withZone(zone: ZoneId): Clock = this
}
