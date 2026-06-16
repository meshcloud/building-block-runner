terraform {
  backend "http" {
    broken = " # missing quote makes it broken
  }
}
