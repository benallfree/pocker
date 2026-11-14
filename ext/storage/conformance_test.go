package storage_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
	"github.com/pockethost/pocketone/ext/storage"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return storage.New(storage.Config{}) })
}
