package bulkmail

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// SNSMessage is an Amazon SNS HTTP(S) delivery.
type SNSMessage struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	Token            string `json:"Token"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
}

var snsHostRe = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

func validSNSURL(raw string, wantPEM bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !snsHostRe.MatchString(u.Hostname()) || u.Port() != "" {
		return false
	}
	return !wantPEM || strings.HasSuffix(u.Path, ".pem")
}

// stringToSign builds the canonical string SNS signs.
func (m *SNSMessage) stringToSign() string {
	var b strings.Builder
	add := func(k, v string) { b.WriteString(k + "\n" + v + "\n") }
	add("Message", m.Message)
	add("MessageId", m.MessageId)
	if m.Type == "Notification" {
		if m.Subject != "" {
			add("Subject", m.Subject)
		}
		add("Timestamp", m.Timestamp)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
		return b.String()
	}
	add("SubscribeURL", m.SubscribeURL)
	add("Timestamp", m.Timestamp)
	add("Token", m.Token)
	add("TopicArn", m.TopicArn)
	add("Type", m.Type)
	return b.String()
}

// VerifySNS checks an SNS message's signature against its signing certificate, which must come from an
// sns.<region>.amazonaws.com host over HTTPS.
func (bm *BulkMail) VerifySNS(m *SNSMessage) error {
	if !validSNSURL(m.SigningCertURL, true) {
		return errors.New("untrusted SigningCertURL")
	}
	cert, err := bm.snsCert(m.SigningCertURL)
	if err != nil {
		return err
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("unexpected SNS key type")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return errors.New("bad signature encoding")
	}
	msg := []byte(m.stringToSign())
	switch m.SignatureVersion {
	case "1":
		h := sha1.Sum(msg)
		return rsa.VerifyPKCS1v15(pub, crypto.SHA1, h[:], sig)
	case "2":
		h := sha256.Sum256(msg)
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
	}
	return fmt.Errorf("unsupported SignatureVersion %q", m.SignatureVersion)
}

func (bm *BulkMail) snsCert(certURL string) (*x509.Certificate, error) {
	if c, ok := bm.certs.Load(certURL); ok {
		return c.(*x509.Certificate), nil
	}
	raw, err := bm.fetchPEM(certURL)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("bad SNS certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	bm.certs.Store(certURL, cert)
	return cert, nil
}

func (bm *BulkMail) httpGetPEM(u string) ([]byte, error) {
	resp, err := bm.cfg.HTTPClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

// HandleSNS verifies and processes one SNS delivery: subscription confirmations are confirmed, SES bounce and
// complaint notifications become suppressions.
func (bm *BulkMail) HandleSNS(m *SNSMessage) error {
	if err := bm.VerifySNS(m); err != nil {
		return fmt.Errorf("SNS signature: %w", err)
	}
	s, err := bm.Settings()
	if err != nil {
		return err
	}
	if len(s.SNSTopicARNs) > 0 && !slices.Contains(s.SNSTopicARNs, m.TopicArn) {
		return errors.New("SNS topic not allowed in Bulk mail settings")
	}

	switch m.Type {
	case "SubscriptionConfirmation":
		if !validSNSURL(m.SubscribeURL, false) {
			return errors.New("untrusted SubscribeURL")
		}
		resp, err := bm.cfg.HTTPClient.Get(m.SubscribeURL)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	case "UnsubscribeConfirmation":
		return nil
	case "Notification":
		return bm.processSES([]byte(m.Message), "ses")
	}
	return fmt.Errorf("unknown SNS type %q", m.Type)
}

type sesNotification struct {
	NotificationType string `json:"notificationType"`
	EventType        string `json:"eventType"`
	Bounce           *struct {
		BounceType        string `json:"bounceType"`
		BouncedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint *struct {
		ComplainedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
	} `json:"complaint"`
	Mail struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	} `json:"mail"`
}

func (bm *BulkMail) processSES(raw []byte, source string) error {
	var n sesNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		return fmt.Errorf("SES notification: %w", err)
	}
	campaignId := ""
	for _, h := range n.Mail.Headers {
		if strings.EqualFold(h.Name, "X-Bulkmail-Campaign") {
			campaignId = h.Value
		}
	}
	kind := n.NotificationType
	if kind == "" {
		kind = n.EventType
	}
	var errs []error
	switch kind {
	case "Bounce":
		if n.Bounce == nil || n.Bounce.BounceType != "Permanent" {
			return nil // transient bounces don't suppress
		}
		for _, r := range n.Bounce.BouncedRecipients {
			errs = append(errs, bm.Feedback(r.EmailAddress, ReasonBounced, source, campaignId))
		}
	case "Complaint":
		if n.Complaint == nil {
			return nil
		}
		for _, r := range n.Complaint.ComplainedRecipients {
			errs = append(errs, bm.Feedback(r.EmailAddress, ReasonComplained, source, campaignId))
		}
	}
	return errors.Join(errs...)
}
