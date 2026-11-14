package autotls_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/autotls"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return autotls.NewApp(autotls.Config{}) })
}
