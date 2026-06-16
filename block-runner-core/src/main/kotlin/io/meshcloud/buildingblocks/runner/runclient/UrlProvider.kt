package io.meshcloud.buildingblocks.runner.runclient

interface UrlProvider {
  fun getRegisterSourceUrl(): String

  fun getUpdateSourceUrl(): String
}
