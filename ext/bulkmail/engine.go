package bulkmail

import (
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

const (
	maxAttempts      = 3
	brakeMinSends    = 100
	brakeFailureRate = 0.05
	idleTick         = 15 * time.Second
)

// Start queues a campaign's audience and starts (or schedules) sending. Starting a finished campaign again queues
// only addresses that aren't in its ledger yet ("send to new matches").
func (bm *BulkMail) Start(id string) (*core.Record, error) {
	return bm.StartLimit(id, 0)
}

// StartLimit is [BulkMail.Start] that queues at most limit new recipients (0 = everyone), for sending a campaign in
// steps: each call adds the next limit addresses that aren't in the ledger yet.
func (bm *BulkMail) StartLimit(id string, limit int) (*core.Record, error) {
	c, err := bm.campaign(id)
	if err != nil {
		return nil, err
	}
	if bm.cfg.Daily == NoBulkMail {
		return nil, errNotAllowed
	}
	switch c.GetString("status") {
	case StatusSending:
		return c, nil
	case StatusCanceled:
		return nil, errors.New("this campaign was canceled; duplicate it to send again")
	}
	if err := bm.validateCampaign(c); err != nil {
		return nil, err
	}

	now := bm.cfg.Now().UTC()
	if at := c.GetDateTime("scheduled_at"); !at.IsZero() && at.Time().After(now) {
		c.Set("status", StatusScheduled)
		c.Set("status_reason", "")
		if err := bm.app.Save(c); err != nil {
			return nil, err
		}
		bm.poke()
		return c, nil
	}
	return bm.queueAndSend(c, limit)
}

func (bm *BulkMail) queueAndSend(c *core.Record, limit int) (*core.Record, error) {
	bm.forgetVars(c.Id)
	if _, err := bm.resolveVars(c); err != nil {
		return nil, err
	}
	if err := bm.queue(c, limit); err != nil {
		return nil, err
	}
	c.Set("status", StatusSending)
	c.Set("status_reason", "")
	if c.GetDateTime("started_at").IsZero() {
		c.Set("started_at", bm.cfg.Now().UTC())
	}
	c.Set("finished_at", "")
	if err := bm.refreshCounters(c); err != nil {
		return nil, err
	}
	bm.poke()
	return c, nil
}

// Pause stops sending (or a schedule) until Resume.
func (bm *BulkMail) Pause(id string) (*core.Record, error) {
	c, err := bm.campaign(id)
	if err != nil {
		return nil, err
	}
	switch c.GetString("status") {
	case StatusSending, StatusScheduled:
		return c, bm.setStatus(c, StatusPaused, "Paused by a superuser.")
	}
	return nil, fmt.Errorf("can't pause a %s campaign", c.GetString("status"))
}

// Resume continues a paused campaign.
func (bm *BulkMail) Resume(id string) (*core.Record, error) {
	c, err := bm.campaign(id)
	if err != nil {
		return nil, err
	}
	if c.GetString("status") != StatusPaused {
		return nil, fmt.Errorf("can't resume a %s campaign", c.GetString("status"))
	}
	if bm.cfg.Daily == NoBulkMail {
		return nil, errNotAllowed
	}
	if c.GetDateTime("started_at").IsZero() { // paused while scheduled
		c.Set("status", StatusDraft)
		return bm.Start(c.Id)
	}
	if err := bm.setStatus(c, StatusSending, ""); err != nil {
		return nil, err
	}
	bm.poke()
	return c, nil
}

// Cancel stops a campaign for good; queued recipients are marked skipped.
func (bm *BulkMail) Cancel(id string) (*core.Record, error) {
	c, err := bm.campaign(id)
	if err != nil {
		return nil, err
	}
	switch c.GetString("status") {
	case StatusDone, StatusCanceled:
		return nil, fmt.Errorf("can't cancel a %s campaign", c.GetString("status"))
	}
	_, err = bm.app.DB().Update(SendsCollection,
		dbx.Params{"status": SendSkipped, "reason": "campaign canceled", "updated": types.NowDateTime().String()},
		dbx.HashExp{"campaign": c.Id, "status": SendQueued}).Execute()
	if err != nil {
		return nil, err
	}
	c.Set("finished_at", bm.cfg.Now().UTC())
	c.Set("status", StatusCanceled)
	c.Set("status_reason", "Canceled by a superuser.")
	return c, bm.refreshCounters(c)
}

// Preview renders the campaign for one audience member (the first match, or the given address if it matches).
func (bm *BulkMail) Preview(id string, email string) (*mailer.Message, error) {
	c, err := bm.campaign(id)
	if err != nil {
		return nil, err
	}
	r, err := bm.pickRecipient(c, email)
	if err != nil {
		return nil, err
	}
	return bm.buildMessage(c, r)
}

// Test sends the campaign, rendered for one audience member, to to (default: the campaign's test_to). It writes
// no ledger rows and ignores suppressions, limits and status.
func (bm *BulkMail) Test(id string, to string) error {
	c, err := bm.campaign(id)
	if err != nil {
		return err
	}
	if to == "" {
		to = c.GetString("test_to")
	}
	var addrs []mail.Address
	for _, a := range strings.Split(to, ",") {
		if e := NormalizeEmail(a); e != "" {
			addrs = append(addrs, mail.Address{Address: e})
		}
	}
	if len(addrs) == 0 {
		return errors.New("set a test address (test_to)")
	}
	bm.forgetVars(c.Id)
	r, err := bm.pickRecipient(c, "")
	if err != nil {
		return err
	}
	msg, err := bm.buildMessage(c, r)
	if err != nil {
		return err
	}
	msg.To = addrs
	msg.Subject = "[TEST] " + msg.Subject
	return bm.mailClient().Send(msg)
}

func (bm *BulkMail) pickRecipient(c *core.Record, email string) (Recipient, error) {
	a, err := ParseAudience(c.Get("audience"))
	if err != nil {
		return Recipient{}, err
	}
	email = NormalizeEmail(email)
	var found *Recipient
	errFound := errors.New("found")
	err = bm.EachRecipient(a, func(r Recipient) error {
		e := NormalizeEmail(r.Email)
		if e == "" || (email != "" && e != email) {
			return nil
		}
		r.Email = e
		found = &r
		return errFound
	})
	if err != nil && !errors.Is(err, errFound) {
		return Recipient{}, err
	}
	if found == nil {
		if email != "" {
			return Recipient{Email: email, Data: map[string]any{"email": email}}, nil
		}
		return Recipient{}, errors.New("the audience matches nobody")
	}
	return *found, nil
}

func (bm *BulkMail) validateCampaign(c *core.Record) error {
	if strings.TrimSpace(c.GetString("subject")) == "" {
		return errors.New("the campaign needs a subject")
	}
	if strings.TrimSpace(c.GetString("html")) == "" && strings.TrimSpace(c.GetString("text")) == "" {
		return errors.New("the campaign needs a body")
	}
	if _, err := ParseAudience(c.Get("audience")); err != nil {
		return err
	}
	for _, f := range []string{"subject", "html", "text"} {
		if _, err := parseTemplate(c.GetString(f)); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	if from, _ := bm.from(c); from.Address == "" {
		return errors.New("set a sender address (campaign, Bulk mail settings or Mail settings)")
	}
	return nil
}

// queue resolves the audience into ledger rows. Existing rows (any status) are left alone.
func (bm *BulkMail) queue(c *core.Record, limit int) error {
	a, err := ParseAudience(c.Get("audience"))
	if err != nil {
		return err
	}
	sup, err := bm.suppressedSet()
	if err != nil {
		return err
	}
	var existing []string
	if err := bm.app.DB().Select("email").From(SendsCollection).
		Where(dbx.HashExp{"campaign": c.Id}).Column(&existing); err != nil {
		return err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[e] = true
	}

	sends, err := bm.app.FindCollectionByNameOrId(SendsId)
	if err != nil {
		return err
	}
	queued := 0
	errLimit := errors.New("limit")

	var batch []*core.Record
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := bm.app.RunInTransaction(func(tx core.App) error {
			for _, r := range batch {
				if err := tx.SaveNoValidate(r); err != nil {
					return err
				}
			}
			return nil
		})
		batch = batch[:0]
		return err
	}

	err = bm.EachRecipient(a, func(r Recipient) error {
		e := NormalizeEmail(r.Email)
		if e == "" || seen[e] {
			return nil
		}
		seen[e] = true
		rec := core.NewRecord(sends)
		rec.Set("campaign", c.Id)
		rec.Set("email", e)
		rec.Set("record_ref", r.RecordRef)
		rec.Set("status", SendQueued)
		if sup[e] {
			rec.Set("status", SendSkipped)
			rec.Set("reason", "suppressed")
		} else {
			queued++
		}
		batch = append(batch, rec)
		if limit > 0 && queued >= limit {
			if err := flush(); err != nil {
				return err
			}
			return errLimit
		}
		if len(batch) >= 200 {
			return flush()
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLimit) {
		return err
	}
	return flush()
}

// refreshCounters recounts the ledger into the campaign record and saves it.
func (bm *BulkMail) refreshCounters(c *core.Record) error {
	var rows []struct {
		Status string `db:"status"`
		N      int    `db:"n"`
	}
	err := bm.app.DB().Select("status", "COUNT(*) AS n").From(SendsCollection).
		Where(dbx.HashExp{"campaign": c.Id}).GroupBy("status").All(&rows)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status] = r.N
	}
	c.Set("queued", counts[SendQueued])
	c.Set("sent", counts[SendSent])
	c.Set("skipped", counts[SendSkipped])
	c.Set("failed", counts[SendFailed])
	return bm.app.Save(c)
}

func (bm *BulkMail) setStatus(c *core.Record, status, reason string) error {
	c.Set("status", status)
	c.Set("status_reason", reason)
	if status == StatusDone {
		c.Set("finished_at", bm.cfg.Now().UTC())
	}
	return bm.app.Save(c)
}

func (bm *BulkMail) campaign(id string) (*core.Record, error) {
	c, err := bm.app.FindRecordById(CampaignsId, id)
	if err != nil {
		return nil, fmt.Errorf("campaign %q not found", id)
	}
	return c, nil
}

func (bm *BulkMail) from(c *core.Record) (mail.Address, error) {
	s, err := bm.Settings()
	if err != nil {
		return mail.Address{}, err
	}
	addr := mail.Address{Name: c.GetString("from_name"), Address: c.GetString("from_address")}
	if addr.Address == "" {
		addr.Address = s.FromAddress
		if addr.Name == "" {
			addr.Name = s.FromName
		}
	}
	if addr.Address == "" {
		addr.Address = bm.app.Settings().Meta.SenderAddress
		if addr.Name == "" {
			addr.Name = bm.app.Settings().Meta.SenderName
		}
	}
	return addr, nil
}

// buildMessage renders one recipient's message with the footer and List-Unsubscribe headers.
func (bm *BulkMail) buildMessage(c *core.Record, r Recipient) (*mailer.Message, error) {
	s, err := bm.Settings()
	if err != nil {
		return nil, err
	}
	vars, err := bm.resolveVars(c)
	if err != nil {
		return nil, err
	}
	from, err := bm.from(c)
	if err != nil {
		return nil, err
	}

	unsub := bm.UnsubscribeURL(r.Email, c.Id)
	data := map[string]any{}
	for k, v := range vars {
		data[k] = v
	}
	record := r.Data
	if record == nil {
		record = map[string]any{}
	}
	data["record"] = record
	data["email"] = r.Email
	data["unsubscribe_url"] = unsub
	data["campaign"] = map[string]any{"id": c.Id, "name": c.GetString("name")}

	subject, err := renderTemplate(c.GetString("subject"), data, false)
	if err != nil {
		return nil, fmt.Errorf("subject: %w", err)
	}
	htmlBody, err := renderTemplate(c.GetString("html"), data, true)
	if err != nil {
		return nil, fmt.Errorf("html: %w", err)
	}
	text, err := renderTemplate(c.GetString("text"), data, false)
	if err != nil {
		return nil, fmt.Errorf("text: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		text = htmlToText(htmlBody)
	}
	if strings.TrimSpace(htmlBody) != "" {
		htmlBody += footerHTML(unsub, s.PostalAddress)
	}
	text += footerText(unsub, s.PostalAddress)

	listUnsub := "<" + unsub + ">"
	if s.UnsubscribeMailto != "" {
		listUnsub += ", <mailto:" + s.UnsubscribeMailto + "?subject=unsubscribe>"
	}
	domain := "localhost"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}

	return &mailer.Message{
		From:    from,
		To:      []mail.Address{{Address: r.Email}},
		Subject: strings.TrimSpace(strings.ReplaceAll(subject, "\n", " ")),
		HTML:    htmlBody,
		Text:    text,
		Headers: map[string]string{
			"List-Unsubscribe":      listUnsub,
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			"Message-ID":            "<bm." + security.PseudorandomString(20) + "@" + domain + ">",
			"X-Bulkmail-Campaign":   c.Id,
		},
	}, nil
}

// --- the sender -------------------------------------------------------------------------------------------------

// StartSender runs the background sender (idempotent). [Register] calls it on serve.
func (bm *BulkMail) StartSender() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.stop != nil {
		return
	}
	bm.stop = make(chan struct{})
	bm.stopped = make(chan struct{})
	go bm.loop(bm.stop, bm.stopped)
}

// StopSender ends the background sender and waits for the message in flight.
func (bm *BulkMail) StopSender() {
	bm.mu.Lock()
	stop, stopped := bm.stop, bm.stopped
	bm.stop, bm.stopped = nil, nil
	bm.mu.Unlock()
	if stop != nil {
		close(stop)
		<-stopped
	}
}

func (bm *BulkMail) poke() {
	select {
	case bm.wake <- struct{}{}:
	default:
	}
}

func (bm *BulkMail) loop(stop, stopped chan struct{}) {
	defer close(stopped)
	for {
		wait, err := bm.Step()
		if err != nil {
			bm.app.Logger().Error("bulkmail: sender", "error", err)
			wait = idleTick
		}
		t := time.NewTimer(wait)
		select {
		case <-stop:
			t.Stop()
			return
		case <-bm.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// Step does one unit of sender work (start a due scheduled campaign, or send one message) and returns how long to
// wait before the next. Exported so hosts and tests can drive the sender without the goroutine.
func (bm *BulkMail) Step() (time.Duration, error) {
	now := bm.cfg.Now().UTC()

	// due scheduled campaigns start now
	due, err := bm.app.FindRecordsByFilter(CampaignsId, "status = 'scheduled' && scheduled_at <= {:now}",
		"scheduled_at", 1, 0, dbx.Params{"now": now.Format(types.DefaultDateLayout)})
	if err != nil {
		return 0, err
	}
	if len(due) > 0 {
		if _, err := bm.queueAndSend(due[0], 0); err != nil {
			_ = bm.setStatus(due[0], StatusPaused, "Couldn't start: "+err.Error())
		}
		return 0, nil
	}

	sending, err := bm.app.FindRecordsByFilter(CampaignsId, "status = 'sending'", "started_at", 0, 0)
	if err != nil {
		return 0, err
	}
	if len(sending) == 0 {
		return idleTick, nil
	}

	if bm.cfg.Daily == NoBulkMail {
		return idleTick, nil
	}
	sentToday, err := bm.sentToday()
	if err != nil {
		return 0, err
	}
	if sentToday >= bm.cfg.Daily {
		tomorrow := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		for _, c := range sending {
			if !strings.HasPrefix(c.GetString("status_reason"), "Daily limit") {
				_ = bm.setStatus(c, StatusSending, fmt.Sprintf("Daily limit of %d reached; continues after midnight UTC.", bm.cfg.Daily))
			}
		}
		return min(tomorrow.Sub(now), idleTick*4), nil
	}

	// oldest running campaign first, one message per step
	for _, c := range sending {
		send, err := bm.nextSend(c.Id, now)
		if err != nil {
			return 0, err
		}
		if send == nil {
			if pending, err := bm.hasQueued(c.Id); err != nil {
				return 0, err
			} else if !pending {
				if err := bm.refreshCounters(c); err != nil {
					return 0, err
				}
				if err := bm.setStatus(c, StatusDone, ""); err != nil {
					return 0, err
				}
			}
			continue // only rows waiting for a retry
		}
		if strings.HasPrefix(c.GetString("status_reason"), "Daily limit") {
			c.Set("status_reason", "")
		}
		bm.sendOne(c, send)
		return bm.interval(c), nil
	}
	return idleTick, nil // only rows waiting for a retry (backoff is minutes)
}

func (bm *BulkMail) interval(c *core.Record) time.Duration {
	rate := bm.cfg.Rate
	if r := c.GetFloat("rate_per_sec"); r > 0 && r < rate {
		rate = r
	}
	return time.Duration(float64(time.Second) / rate)
}

func (bm *BulkMail) nextSend(campaignId string, now time.Time) (*core.Record, error) {
	rows, err := bm.app.FindRecordsByFilter(SendsId,
		"campaign = {:c} && status = 'queued' && (retry_at = '' || retry_at <= {:now})", "created", 1, 0,
		dbx.Params{"c": campaignId, "now": now.Format(types.DefaultDateLayout)})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (bm *BulkMail) hasQueued(campaignId string) (bool, error) {
	var n int
	err := bm.app.DB().Select("COUNT(*)").From(SendsCollection).
		Where(dbx.HashExp{"campaign": campaignId, "status": SendQueued}).Row(&n)
	return n > 0, err
}

func (bm *BulkMail) sentToday() (int, error) {
	start := bm.cfg.Now().UTC().Truncate(24 * time.Hour)
	var n int
	err := bm.app.DB().Select("COUNT(*)").From(SendsCollection).
		Where(dbx.HashExp{"status": SendSent}).
		AndWhere(dbx.NewExp("sent_at >= {:start}", dbx.Params{"start": start.Format(types.DefaultDateLayout)})).
		Row(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (bm *BulkMail) sendOne(c *core.Record, send *core.Record) {
	now := bm.cfg.Now().UTC()
	email := send.GetString("email")
	send.Set("attempts", send.GetInt("attempts")+1)

	fail := func(err error, permanent bool) {
		send.Set("reason", err.Error())
		if permanent || send.GetInt("attempts") >= maxAttempts {
			send.Set("status", SendFailed)
		} else {
			backoff := time.Duration(send.GetInt("attempts")*send.GetInt("attempts")) * time.Minute
			send.Set("retry_at", now.Add(backoff))
		}
		bm.saveSend(c, send)
	}

	// a suppression added after queueing still wins
	if sup, _ := bm.IsSuppressed(email); sup {
		send.Set("status", SendSkipped)
		send.Set("reason", "suppressed")
		bm.saveSend(c, send)
		return
	}

	r := Recipient{Email: email, RecordRef: send.GetString("record_ref"), Data: map[string]any{"email": email}}
	if col, id, ok := strings.Cut(r.RecordRef, ":"); ok && col != "sql" {
		if rec, err := bm.app.FindRecordById(col, id); err == nil {
			data := rec.PublicExport()
			for _, f := range rec.Collection().Fields {
				if f.Type() == core.FieldTypePassword || f.GetName() == core.FieldNameTokenKey {
					continue
				}
				data[f.GetName()] = rec.Get(f.GetName())
			}
			r.Data = data
		}
	} else if ok && col == "sql" {
		r.Data["id"] = id
	}

	msg, err := bm.buildMessage(c, r)
	if err != nil {
		fail(err, true)
		return
	}

	event := &SendEvent{App: bm.app, Campaign: c, Send: send, Message: msg}
	err = bm.OnSend.Trigger(event, func(e *SendEvent) error {
		return bm.mailClient().Send(e.Message)
	})
	if err != nil {
		fail(err, isPermanent(err))
		return
	}

	send.Set("status", SendSent)
	send.Set("reason", "")
	send.Set("sent_at", now)
	send.Set("message_id", msg.Headers["Message-ID"])
	bm.saveSend(c, send)
}

func (bm *BulkMail) saveSend(c *core.Record, send *core.Record) {
	if err := bm.app.Save(send); err != nil {
		bm.app.Logger().Error("bulkmail: save send", "error", err, "send", send.Id)
	}
	if err := bm.refreshCounters(c); err != nil {
		bm.app.Logger().Error("bulkmail: counters", "error", err, "campaign", c.Id)
		return
	}
	// auto-brake: stop a campaign that's mostly failing before it burns the sender's reputation
	done := c.GetInt("sent") + c.GetInt("failed")
	if done >= brakeMinSends && float64(c.GetInt("failed"))/float64(done) > brakeFailureRate {
		_ = bm.setStatus(c, StatusPaused, fmt.Sprintf("Paused automatically: %d of %d sends failed. Check the failures, then resume.",
			c.GetInt("failed"), done))
	}
}

// isPermanent reports whether an SMTP error is a 5xx (don't retry).
func isPermanent(err error) bool {
	var tp *textproto.Error
	if errors.As(err, &tp) {
		return tp.Code >= 500
	}
	return false
}
