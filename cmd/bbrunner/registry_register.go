//go:build !k8s

package main

import "fmt"

// registerType adds a linked runner type's registration to typeRegistry. It lives in a `!k8s`
// file because only the runner-type files (tf.go, manual.go, ... — themselves `!k8s`) call it
// from their init(): the `-tags k8s` lean controller links no type handlers, so it links no
// caller either, and keeping registerType out of the always-compiled registry.go avoids a
// dead-symbol under that build. Panics on a duplicate token, which can only be a wiring bug (two
// files registering the same token), never user input.
func registerType(token runnerType, reg typeRegistration) {
	if _, exists := typeRegistry[token]; exists {
		panic(fmt.Sprintf("bbrunner: runner type %q registered twice", token))
	}
	typeRegistry[token] = reg
}
