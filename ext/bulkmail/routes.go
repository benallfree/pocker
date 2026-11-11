package bulkmail

import (
	"crypto/subtle"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

func (bm *BulkMail) bindRoutes(se *core.ServeEvent) {
	g := se.Router.Group("/api/bulkmail")

	su := g.Group("")
	su.Bind(apis.RequireSuperuserAuth())
	su.GET("/settings", bm.handleGetSettings)
	su.PATCH("/settings", bm.handleSaveSettings)
	su.POST("/audience/count", bm.handleCount)
	su.POST("/campaigns/{id}/start", bm.handleStart)
	su.POST("/campaigns/{id}/pause", bm.campaignAction(bm.Pause))
	su.POST("/campaigns/{id}/resume", bm.campaignAction(bm.Resume))
	su.POST("/campaigns/{id}/cancel", bm.campaignAction(bm.Cancel))
	su.POST("/campaigns/{id}/test", bm.handleTest)
	su.POST("/campaigns/{id}/preview", bm.handlePreview)

	// public: signed links and provider callbacks
	g.GET("/unsubscribe", bm.handleUnsubscribePage)
	g.POST("/unsubscribe", bm.handleUnsubscribe)
	g.POST("/feedback/ses", bm.handleSES)
	g.POST("/feedback/webhook", bm.handleWebhook)
}

type settingsResponse struct {
	Settings
	HasWebhookSecret bool    `json:"hasWebhookSecret"`
	Rate             float64 `json:"rate"`
	Daily            int     `json:"daily"`
	SentToday        int     `json:"sentToday"`
	Allowed          bool    `json:"allowed"`
	SESEndpoint      string  `json:"sesEndpoint"`
	WebhookEndpoint  string  `json:"webhookEndpoint"`
}

func (bm *BulkMail) settingsResponse() (*settingsResponse, error) {
	s, err := bm.Settings()
	if err != nil {
		return nil, err
	}
	rate, daily, sent, err := bm.Limits()
	if err != nil {
		return nil, err
	}
	res := &settingsResponse{
		Settings:         s,
		HasWebhookSecret: s.WebhookSecret != "",
		Rate:             rate,
		Daily:            daily,
		SentToday:        sent,
		Allowed:          daily != NoBulkMail,
		SESEndpoint:      bm.publicURL() + "/api/bulkmail/feedback/ses",
		WebhookEndpoint:  bm.publicURL() + "/api/bulkmail/feedback/webhook",
	}
	res.Secret, res.WebhookSecret = "", ""
	return res, nil
}

func (bm *BulkMail) handleGetSettings(e *core.RequestEvent) error {
	res, err := bm.settingsResponse()
	if err != nil {
		return e.InternalServerError("", err)
	}
	return e.JSON(http.StatusOK, res)
}

func (bm *BulkMail) handleSaveSettings(e *core.RequestEvent) error {
	cur, err := bm.Settings()
	if err != nil {
		return e.InternalServerError("", err)
	}
	cur.Secret, cur.WebhookSecret = "", "" // empty = keep
	if err := json.NewDecoder(io.LimitReader(e.Request.Body, 1<<16)).Decode(&cur); err != nil {
		return e.BadRequestError("Invalid settings.", err)
	}
	if err := bm.SaveSettings(cur); err != nil {
		return e.InternalServerError("", err)
	}
	return bm.handleGetSettings(e)
}

func (bm *BulkMail) handleCount(e *core.RequestEvent) error {
	var body struct {
		Audience json.RawMessage `json:"audience"`
	}
	if err := json.NewDecoder(io.LimitReader(e.Request.Body, 1<<16)).Decode(&body); err != nil {
		return e.BadRequestError("Invalid body.", err)
	}
	a, err := ParseAudience([]byte(body.Audience))
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	n, sup, sample, err := bm.CountAudience(a, 5)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.JSON(http.StatusOK, map[string]any{"count": n, "suppressed": sup, "sample": sample})
}

func (bm *BulkMail) campaignAction(fn func(id string) (*core.Record, error)) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		c, err := fn(e.Request.PathValue("id"))
		if err != nil {
			return e.BadRequestError(err.Error(), nil)
		}
		return e.JSON(http.StatusOK, c)
	}
}

// handleStart takes an optional {"limit": N}: queue at most N new recipients (0 = everyone).
func (bm *BulkMail) handleStart(e *core.RequestEvent) error {
	var body struct {
		Limit int `json:"limit"`
	}
	_ = json.NewDecoder(io.LimitReader(e.Request.Body, 1<<12)).Decode(&body)
	c, err := bm.StartLimit(e.Request.PathValue("id"), max(body.Limit, 0))
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.JSON(http.StatusOK, c)
}

func (bm *BulkMail) handleTest(e *core.RequestEvent) error {
	var body struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(io.LimitReader(e.Request.Body, 1<<12)).Decode(&body)
	if err := bm.Test(e.Request.PathValue("id"), body.To); err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.NoContent(http.StatusNoContent)
}

func (bm *BulkMail) handlePreview(e *core.RequestEvent) error {
	var body struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(io.LimitReader(e.Request.Body, 1<<12)).Decode(&body)
	msg, err := bm.Preview(e.Request.PathValue("id"), body.Email)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.JSON(http.StatusOK, map[string]any{
		"to":      msg.To[0].Address,
		"from":    msg.From.String(),
		"subject": msg.Subject,
		"html":    msg.HTML,
		"text":    msg.Text,
		"headers": msg.Headers,
	})
}

// --- public ----------------------------------------------------------------------------------------------------

func (bm *BulkMail) handleUnsubscribePage(e *core.RequestEvent) error {
	token := e.Request.URL.Query().Get("t")
	email, _, err := bm.ParseUnsubscribeToken(token)
	if err != nil {
		return e.HTML(http.StatusBadRequest, page("Invalid link", "<p>This unsubscribe link isn't valid.</p>"))
	}
	if sup, _ := bm.IsSuppressed(email); sup {
		return e.HTML(http.StatusOK, page("Unsubscribed", "<p>"+html.EscapeString(email)+" won't receive these emails anymore.</p>"))
	}
	// a GET never unsubscribes: link scanners open every URL in a message
	return e.HTML(http.StatusOK, page("Unsubscribe",
		`<p>Stop sending these emails to <b>`+html.EscapeString(email)+`</b>?</p>`+
			`<form method="post"><input type="hidden" name="t" value="`+html.EscapeString(token)+`">`+
			`<button type="submit">Unsubscribe</button></form>`))
}

// handleUnsubscribe serves both the confirm form and RFC 8058 one-click POSTs from mail providers.
func (bm *BulkMail) handleUnsubscribe(e *core.RequestEvent) error {
	token := e.Request.URL.Query().Get("t")
	oneClick := false
	if err := e.Request.ParseForm(); err == nil {
		if t := e.Request.PostForm.Get("t"); t != "" {
			token = t
		}
		oneClick = e.Request.PostForm.Get("List-Unsubscribe") == "One-Click"
	}
	email, campaignId, err := bm.ParseUnsubscribeToken(token)
	if err != nil {
		return e.HTML(http.StatusBadRequest, page("Invalid link", "<p>This unsubscribe link isn't valid.</p>"))
	}
	source := "link"
	if oneClick {
		source = "one-click"
	}
	if err := bm.Unsubscribe(email, campaignId, source); err != nil {
		return e.InternalServerError("", err)
	}
	return e.HTML(http.StatusOK, page("Unsubscribed", "<p>"+html.EscapeString(email)+" won't receive these emails anymore.</p>"))
}

func (bm *BulkMail) handleSES(e *core.RequestEvent) error {
	var m SNSMessage
	if err := json.NewDecoder(io.LimitReader(e.Request.Body, 256<<10)).Decode(&m); err != nil {
		return e.BadRequestError("Invalid SNS message.", nil)
	}
	if err := bm.HandleSNS(&m); err != nil {
		e.App.Logger().Warn("bulkmail: SES feedback rejected", "error", err)
		return e.BadRequestError("Rejected.", nil)
	}
	return e.NoContent(http.StatusNoContent)
}

// handleWebhook takes {"type":"bounce|complaint|unsubscribe","email":"…","campaign":"…"} with the
// X-Bulkmail-Secret header, for providers without a dedicated adapter.
func (bm *BulkMail) handleWebhook(e *core.RequestEvent) error {
	s, err := bm.Settings()
	if err != nil {
		return e.InternalServerError("", err)
	}
	given := e.Request.Header.Get("X-Bulkmail-Secret")
	if s.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(given), []byte(s.WebhookSecret)) != 1 {
		return e.UnauthorizedError("", nil)
	}
	var body struct {
		Type     string `json:"type"`
		Email    string `json:"email"`
		Campaign string `json:"campaign"`
	}
	if err := json.NewDecoder(io.LimitReader(e.Request.Body, 1<<14)).Decode(&body); err != nil {
		return e.BadRequestError("Invalid body.", nil)
	}
	switch strings.ToLower(body.Type) {
	case "bounce":
		err = bm.Feedback(body.Email, ReasonBounced, "webhook", body.Campaign)
	case "complaint":
		err = bm.Feedback(body.Email, ReasonComplained, "webhook", body.Campaign)
	case "unsubscribe":
		err = bm.Unsubscribe(body.Email, body.Campaign, "webhook")
	default:
		return e.BadRequestError("type must be bounce, complaint or unsubscribe", nil)
	}
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.NoContent(http.StatusNoContent)
}

func page(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;color:#222}` +
		`button{font:inherit;padding:.5rem 1.25rem;border:0;border-radius:6px;background:#222;color:#fff;cursor:pointer}</style></head><body>` +
		`<h1>` + html.EscapeString(title) + `</h1>` + body + `</body></html>`
}
