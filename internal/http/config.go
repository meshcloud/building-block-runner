package http

import (
	"errors"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func NewAuthorization(client Client, api config.Api) (Authorization, error) {
	auth := api.Auth
	switch {
	case auth.Token != "":
		return BearerToken(auth.Token), nil
	case auth.Key.ClientId != "" && api.Url.URL == nil:
		return nil, errors.New("api.auth.key needs api.url")
	case auth.Key.ClientId != "":
		return NewApiKeyLogin(client, api.Url.URL, auth.Key.ClientId, auth.Key.ClientSecret), nil
	case auth.Basic.Username != "":
		return BasicAuth{auth.Basic.Username, auth.Basic.Password}, nil
	case auth.Profile != "":
		return nil, errors.New("api.auth.profile is not supported yet")
	}
	return nil, errors.New("api.auth needs either key, basic or a token")
}
