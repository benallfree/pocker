package bulkmail_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/bulkmail"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return bulkmail.New(bulkmail.Config{}) })
}
