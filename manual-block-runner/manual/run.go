package manual

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

const (
	stepId          = "manual"
	stepDisplayName = "Manual Block Run"
	succeeded       = "SUCCEEDED"
)

// Without a timeout, a hanging connection would block the poller and its shutdown forever.
var meshStackHTTP = &http.Client{Timeout: 30 * time.Second}

// ExecuteRunFromFile executes the run that run-controller decrypted and mounted for this process.
func ExecuteRunFromFile(ctx context.Context, cfg Config, runFile string) error {
	data, err := os.ReadFile(runFile)
	if err != nil {
		return err
	}
	run, err := parseRun(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", runFile, err)
	}
	return ExecuteRun(ctx, cfg, run)
}

func ExecuteRun(ctx context.Context, cfg Config, run *meshapi.RunDetailsDTO) error {
	runId := run.Metadata.Uuid
	registerURL, updateURL, err := sourceURLs(run.Links, cfg.Uuid)
	if err != nil {
		return fmt.Errorf("run %s: %w", runId, err)
	}
	client := meshapi.NewClientWithHTTP(cfg.Api.Url, cfg.Uuid, meshapi.BearerTokenAuth{Token: run.Spec.RunToken}, meshStackHTTP)

	err = client.RegisterSourceAt(ctx, registerURL, meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{Id: cfg.Uuid},
		Steps:  []meshapi.StepRegistrationDTO{{Id: stepId, DisplayName: stepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of run %s: %w", runId, err)
	}

	_, err = client.PatchStatusAt(ctx, updateURL, meshapi.StatusUpdateDTO{
		Status: new(succeeded),
		Steps: []meshapi.StepStatusUpdateDTO{{
			Id:          stepId,
			DisplayName: stepDisplayName,
			Status:      new(succeeded),
			Outputs:     outputsFrom(run.Spec.BuildingBlock.Spec.Inputs),
		}},
	})
	if err != nil {
		return fmt.Errorf("report run %s: %w", runId, err)
	}

	slog.Info("reported run as succeeded", "run", runId)
	return nil
}

func sourceURLs(links meshapi.LinksDTO, sourceId string) (registerURL, updateURL string, err error) {
	if links.RegisterSource.Href == "" {
		return "", "", errors.New("the run has no registerSource link")
	}
	if links.UpdateSource.Href == "" {
		return "", "", errors.New("the run has no updateSource link")
	}
	updateURL = strings.ReplaceAll(links.UpdateSource.Href, "{sourceId}", url.PathEscape(sourceId))
	return links.RegisterSource.Href, updateURL, nil
}

func outputsFrom(inputs []meshapi.BuildingBlockInputSpecDTO) map[string]meshapi.OutputDTO {
	outputs := make(map[string]meshapi.OutputDTO, len(inputs))
	for _, input := range inputs {
		outputs[input.Key] = meshapi.OutputDTO{
			Value:     input.Value,
			Type:      outputType(input.Type),
			Sensitive: input.IsSensitive,
		}
	}
	return outputs
}

func outputType(inputType string) string {
	switch inputType {
	case "FILE", "SINGLE_SELECT":
		return "STRING"
	case "LIST", "MULTI_SELECT":
		return "CODE"
	default:
		return inputType
	}
}

// parseRun keeps every number as its JSON text, so that an INTEGER input beyond float64 precision
// comes back as the same output.
func parseRun(data []byte) (*meshapi.RunDetailsDTO, error) {
	keepNumberText := json.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *any) error {
		if dec.PeekKind() != '0' {
			return errors.ErrUnsupported
		}
		number, err := dec.ReadValue()
		if err != nil {
			return err
		}
		*value = number.Clone()
		return nil
	})

	var run meshapi.RunDetailsDTO
	if err := json.Unmarshal(data, &run, json.WithUnmarshalers(keepNumberText)); err != nil {
		return nil, err
	}
	return &run, nil
}
