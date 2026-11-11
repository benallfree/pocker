package bulkmail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// IsSuppressed reports whether email may not receive campaign mail.
func (bm *BulkMail) IsSuppressed(email string) (bool, error) {
	e := NormalizeEmail(email)
	if e == "" {
		return false, nil
	}
	var n int
	err := bm.app.DB().Select("COUNT(*)").From(SuppressionsCollection).Where(dbx.HashExp{"email": e}).Row(&n)
	return n > 0, err
}

// Suppress adds email to the suppression list (or updates its reason). It fires no hooks; the unsubscribe and
// feedback paths do.
func (bm *BulkMail) Suppress(email, reason, source, campaignId string) (*core.Record, error) {
	e := NormalizeEmail(email)
	if e == "" {
		return nil, errors.New("invalid email")
	}
	switch reason {
	case ReasonUnsubscribed, ReasonBounced, ReasonComplained, ReasonManual:
	default:
		return nil, errors.New("invalid reason")
	}
	rec, err := bm.app.FindFirstRecordByData(SuppressionsId, "email", e)
	if err != nil {
		col, cerr := bm.app.FindCollectionByNameOrId(SuppressionsId)
		if cerr != nil {
			return nil, cerr
		}
		rec = core.NewRecord(col)
		rec.Set("email", e)
	}
	rec.Set("reason", reason)
	rec.Set("source", source)
	rec.Set("campaign", campaignId)
	return rec, bm.app.Save(rec)
}

// Unsuppress removes email from the suppression list.
func (bm *BulkMail) Unsuppress(email string) error {
	rec, err := bm.app.FindFirstRecordByData(SuppressionsId, "email", NormalizeEmail(email))
	if err != nil {
		return nil
	}
	return bm.app.Delete(rec)
}

// Unsubscribe suppresses email and fires OnUnsubscribe (source: link, one-click, js, ...).
func (bm *BulkMail) Unsubscribe(email, campaignId, source string) error {
	return bm.suppressWithHook(bm.OnUnsubscribe, email, ReasonUnsubscribed, source, campaignId)
}

// Feedback records a provider's bounce or complaint and fires OnFeedback.
func (bm *BulkMail) Feedback(email, reason, source, campaignId string) error {
	if err := bm.suppressWithHook(bm.OnFeedback, email, reason, source, campaignId); err != nil {
		return err
	}
	if reason == ReasonComplained && campaignId != "" {
		bm.complaintBrake(campaignId)
	}
	return nil
}

func (bm *BulkMail) suppressWithHook(h interface {
	Trigger(*SuppressEvent, ...func(*SuppressEvent) error) error
}, email, reason, source, campaignId string) error {
	e := &SuppressEvent{App: bm.app, Email: NormalizeEmail(email), Reason: reason, Source: source, CampaignId: campaignId}
	if e.Email == "" {
		return errors.New("invalid email")
	}
	return h.Trigger(e, func(e *SuppressEvent) error {
		rec, err := bm.Suppress(e.Email, e.Reason, e.Source, e.CampaignId)
		e.Suppression = rec
		return err
	})
}

// complaintBrake pauses a campaign when complaints pass 0.3% of its sends (and at least 3).
func (bm *BulkMail) complaintBrake(campaignId string) {
	c, err := bm.campaign(campaignId)
	if err != nil || c.GetString("status") != StatusSending {
		return
	}
	var n int
	_ = bm.app.DB().Select("COUNT(*)").From(SuppressionsCollection).
		Where(dbx.HashExp{"campaign": campaignId, "reason": ReasonComplained}).Row(&n)
	sent := c.GetInt("sent")
	if n >= 3 && sent > 0 && float64(n)/float64(sent) > 0.003 {
		_ = bm.setStatus(c, StatusPaused, "Paused automatically: too many spam complaints.")
	}
}

// --- signed unsubscribe links ---------------------------------------------------------------------------------

// UnsubscribeToken signs (email, campaign) with the app's secret.
func (bm *BulkMail) UnsubscribeToken(email, campaignId string) string {
	s, err := bm.Settings()
	if err != nil {
		return ""
	}
	payload := NormalizeEmail(email) + "\n" + campaignId
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write([]byte(payload))
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac.Sum(nil)[:16])
}

// UnsubscribeURL is the link put in every message and in List-Unsubscribe.
func (bm *BulkMail) UnsubscribeURL(email, campaignId string) string {
	return bm.publicURL() + "/api/bulkmail/unsubscribe?t=" + url.QueryEscape(bm.UnsubscribeToken(email, campaignId))
}

// ParseUnsubscribeToken checks a token and returns what it was signed for.
func (bm *BulkMail) ParseUnsubscribeToken(token string) (email, campaignId string, err error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", errors.New("invalid link")
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", "", errors.New("invalid link")
	}
	email, campaignId, _ = strings.Cut(string(payload), "\n")
	if hmac.Equal([]byte(bm.UnsubscribeToken(email, campaignId)), []byte(p+"."+sig)) && NormalizeEmail(email) != "" {
		return email, campaignId, nil
	}
	return "", "", errors.New("invalid link")
}
