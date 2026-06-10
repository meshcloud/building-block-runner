package io.meshcloud.buildingblocks.runner.http

import org.springframework.http.ResponseEntity
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

@RestController
class HealthController {

  @GetMapping("/healthz")
  fun healthz(): ResponseEntity<String> = ResponseEntity.ok("OK")
}
