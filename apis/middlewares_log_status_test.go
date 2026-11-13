package apis_test

// CE: a request whose error isn't an ApiError is logged with the status the client got (the error handler's), not 0.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/logger"
	"github.com/spf13/cast"
)

func TestLogRequestPlainErrorStatus(t *testing.T) {
	t.Parallel()

	statuses := make(chan int, 4)

	scenario := tests.ApiScenario{
		Method: http.MethodGet,
		URL:    "/ce/plain-error",
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, err := tests.NewTestAppWithConfig(core.BaseAppConfig{
				OnLog: func(l *logger.Log) {
					if l.Data["type"] == "request" {
						statuses <- cast.ToInt(l.Data["status"])
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			return app
		},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			app.Settings().Logs.MaxDays = 1 // request logs on (the test app turns them off)
			e.Router.GET("/ce/plain-error", func(e *core.RequestEvent) error {
				return errors.New("plain")
			})
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
			select { // the request log is written on another goroutine
			case status := <-statuses:
				if status != res.StatusCode {
					t.Fatalf("logged status %d, the client got %d", status, res.StatusCode)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no request log entry")
			}
		},
		ExpectedStatus:  400,
		ExpectedContent: []string{`"data":{}`},
	}
	scenario.Test(t)
}
