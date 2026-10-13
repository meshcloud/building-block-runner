package dispatcher

import (
	"errors"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/http"
)

func newAuthorization(client http.Client, api config.Api) (http.Authorization, error) {
	auth := api.Auth
	switch {
	case auth.Key.ClientId != "" && api.Url.URL == nil:
		return nil, errors.New("api.auth.key needs api.url")
	case auth.Key.ClientId != "":
		return http.NewApiKeyLogin(client, api.Url.URL, auth.Key.ClientId, auth.Key.ClientSecret), nil
	case auth.Basic.Username != "":
		return http.BasicAuth{Username: auth.Basic.Username, Password: auth.Basic.Password}, nil
	case auth.Profile != "":
		return nil, errors.New("api.auth.profile is not supported yet")
	}
	return nil, errors.New("api.auth needs either key or basic")
}
