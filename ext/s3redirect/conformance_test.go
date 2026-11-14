package s3redirect_test

import (
	"testing"

	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pluginkit/conformance"
	"github.com/pockethost/pocketone/ext/s3redirect"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, func() pluginkit.Plugin { return s3redirect.New(s3redirect.Config{}) })
}
