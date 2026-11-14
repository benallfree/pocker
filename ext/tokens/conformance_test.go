package tokens_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
	"github.com/pockethost/pocketone/ext/tokens"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return tokens.New(tokens.Config{}) })
}
