//go:build inprocess

package inprocess

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/manual-block-runner/manual"
	"github.com/meshcloud/building-block-runner/run-controller/build"
	"github.com/meshcloud/building-block-runner/run-controller/controller"
	"github.com/meshcloud/building-block-runner/tf-block-runner/tfrun"
)

func init() {
	build.Dispatchers["inprocess"] = func() (build.Dispatcher, error) {
		return NewDispatcher(controller.AppConfig.Uuid, controller.AppConfig.Api.Url)
	}
}

type executeFunc func(ctx context.Context, runJson []byte) error

type Dispatcher struct {
	logger    *log.Logger
	executors map[meshapi.RunnerImplementationType]executeFunc
	active    atomic.Int64
	runs      sync.WaitGroup
}

func NewDispatcher(runnerUuid string, apiUrl string) (*Dispatcher, error) {
	tfrun.AppConfig = tfrun.TfRunnerConfig{
		TfCommandTimeoutMins: 60,
		WsTimeoutMins:        5,
		InitTimeoutMins:      3,
		TfParentWorkingDir:   "/tmp/runner/wd",
		TfInstallDir:         "/tmp/runner/tfbin",
		RunnerUuid:           runnerUuid,
		RunApiBackend:        tfrun.RunApiConfig{Url: apiUrl},
	}
	tfBinaries, err := tfrun.NewTfBin(tfrun.AppConfig.TfInstallDir, os.Stdout)
	if err != nil {
		return nil, err
	}
	tfLogger := log.New(os.Stdout, "[TF RUNNER] ", log.LstdFlags)

	var manualConfig manual.Config
	manualConfig.Uuid = runnerUuid
	manualConfig.Api.Url = apiUrl

	return &Dispatcher{
		logger: log.New(os.Stdout, "[IN-PROCESS] ", log.LstdFlags),
		executors: map[meshapi.RunnerImplementationType]executeFunc{
			meshapi.RunnerTypeTerraform: func(ctx context.Context, runJson []byte) error {
				return tfrun.ExecuteDecryptedRun(ctx, tfLogger, runJson, tfBinaries)
			},
			meshapi.RunnerTypeManual: func(ctx context.Context, runJson []byte) error {
				return manual.ExecuteRunJson(ctx, manualConfig, runJson)
			},
		},
	}, nil
}

func (d *Dispatcher) Dispatch(ctx context.Context, run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error {
	execute, ok := d.executors[meshapi.RunnerImplementationType(runnerType)]
	if !ok {
		return &controller.NoHandlerError{RunnerType: runnerType}
	}
	runJson, err := base64.StdEncoding.DecodeString(decryptedRunJsonBase64)
	if err != nil {
		return err
	}

	d.active.Add(1)
	d.runs.Go(func() { d.execute(ctx, run.Metadata.Uuid, execute, runJson) })
	return nil
}

func (d *Dispatcher) execute(ctx context.Context, runId string, execute executeFunc, runJson []byte) {
	defer d.active.Add(-1)
	defer func() {
		if r := recover(); r != nil {
			d.logger.Printf("Run %s panicked: %v\n%s", runId, r, debug.Stack())
		}
	}()

	if err := execute(ctx, runJson); err != nil {
		d.logger.Printf("Run %s failed: %v", runId, err)
	}
}

func (d *Dispatcher) CountActive() (int, error) {
	return int(d.active.Load()), nil
}

func (d *Dispatcher) Wait() {
	d.logger.Printf("Waiting for %d runs to finish", d.active.Load())
	d.runs.Wait()
}
