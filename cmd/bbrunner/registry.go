package main

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// registry.go is the build-tag-independent seam every linked runner type wires itself through
// ("leaner run-controller image via build tags"): it never imports a runner type's own
// package (internal/tf, internal/manual, ...), so it compiles identically no matter which
// type_<type> / k8s tags are passed. Each linked type's own tag-gated file (tf.go, manual.go,
// gitlab.go, azdevops.go, github.go) calls registerType from an init(); main.go/superset.go
// then only ever walk typeRegistry, so a build that excludes a type via its tag leaves no
// dangling reference to that type's bootstrap or handler builder.

// supersetConn is the controller/superset's single shared connection (uuid, api credentials,
// decryption key) that every linked type's supersetHandler builder receives to construct its
// dispatch.RunHandler (see runControllerSuperset). It carries only scalar/interface fields --
// never a type package's own config/decryptor/binary-provider type -- which is what keeps this
// file import-free of every type package.
type supersetConn struct {
	ApiURL          string
	ApiUsername     string
	ApiPassword     string
	ApiClientId     string
	ApiClientSecret string
	RunnerUuid      string
	PrivateKeyPEM   string
	Log             *slog.Logger
}

// typeRegistration is what one linked runner type contributes to bbrunner.
type typeRegistration struct {
	// implType is the meshapi.RunnerImplementationType this type serves in the superset's
	// dispatch.RunHandler map (see buildSupersetHandlers).
	implType meshapi.RunnerImplementationType
	// fitBootstrap is `bbrunner <type>`'s in-process polling bootstrap -- the same wiring the
	// standalone cmd/<type> binary runs (see newDispatcher).
	fitBootstrap func() int
	// supersetHandler builds this type's dispatch.RunHandler from the controller's shared
	// connection, for the controller/superset's in-process ALL-types dispatch.
	supersetHandler func(conn supersetConn) (dispatch.RunHandler, error)
}

// typeRegistry accumulates every linked runner type's registration, keyed by its fit
// subcommand token (runnerType, e.g. "tf"). init()-populated by each linked type's own
// tag-gated file; empty entries simply never appear when a type's tag excludes its file.
// registerType (the only writer) lives in registry_register.go, gated `!k8s`: only the runner-type
// files call it, and the `-tags k8s` controller links none of them.
var typeRegistry = map[runnerType]typeRegistration{}
