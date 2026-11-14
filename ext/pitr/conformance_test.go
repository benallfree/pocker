package pitr_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/pitr"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return pitr.New(pitr.Config{}) })
}
