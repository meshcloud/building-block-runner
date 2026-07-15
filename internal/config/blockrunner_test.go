package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockRunnerCompat_ApplyShared(t *testing.T) {
	t.Run("empty block leaves flat values untouched", func(t *testing.T) {
		uuid, version := "flat-uuid", "flat-ver"
		api := Api{Url: "flat-url", Username: "flat-user"}
		BlockRunnerCompat{}.ApplyShared(discardLog(), &uuid, &version, &api)
		require.Equal(t, "flat-uuid", uuid)
		require.Equal(t, "flat-ver", version)
		require.Equal(t, "flat-url", api.Url)
		require.Equal(t, "flat-user", api.Username)
	})

	t.Run("block values override flat values", func(t *testing.T) {
		uuid, version := "flat-uuid", "flat-ver"
		api := Api{Url: "flat-url"}
		c := BlockRunnerCompat{Uuid: "block-uuid", Version: "block-ver"}
		c.Api.Url = "block-url"
		c.Auth.Username = "block-user"
		c.Auth.Password = "block-pass"
		c.Auth.ApiKey.ClientId = "block-cid"
		c.Auth.ApiKey.ClientSecret = "block-csecret"
		c.ApplyShared(discardLog(), &uuid, &version, &api)
		require.Equal(t, "block-uuid", uuid)
		require.Equal(t, "block-ver", version)
		require.Equal(t, "block-url", api.Url)
		require.Equal(t, "block-user", api.Username)
		require.Equal(t, "block-pass", api.Password)
		require.Equal(t, "block-cid", api.ClientId)
		require.Equal(t, "block-csecret", api.ClientSecret)
	})

	t.Run("nil api target is tolerated", func(t *testing.T) {
		uuid := ""
		BlockRunnerCompat{Uuid: "u"}.ApplyShared(discardLog(), &uuid, nil, nil)
		require.Equal(t, "u", uuid)
	})
}
