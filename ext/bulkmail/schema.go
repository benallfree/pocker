package bulkmail

import (
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// The schema is a contract: these collection and field ids never change. The legacy PocketHost mothership creates
// the same collections from a JS migration (see [LegacyJSMigration]) before this plugin ever runs there, and
// [EnsureSchema] accepts collections that already exist with these ids.
const (
	CampaignsCollection    = "_bulkmail_campaigns"
	SendsCollection        = "_bulkmail_sends"
	SuppressionsCollection = "_bulkmail_suppressions"

	CampaignsId    = "pbc_bulkmail_campaigns"
	SendsId        = "pbc_bulkmail_sends"
	SuppressionsId = "pbc_bulkmail_suppressions"
)

// Campaign statuses.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusSending   = "sending"
	StatusPaused    = "paused"
	StatusDone      = "done"
	StatusCanceled  = "canceled"
)

// Send statuses.
const (
	SendQueued  = "queued"
	SendSent    = "sent"
	SendSkipped = "skipped"
	SendFailed  = "failed"
)

// Suppression reasons.
const (
	ReasonUnsubscribed = "unsubscribed"
	ReasonBounced      = "bounced"
	ReasonComplained   = "complained"
	ReasonManual       = "manual"
)

// Collections returns the bulk mail collections as this version of the plugin defines them.
func Collections() []*core.Collection {
	return []*core.Collection{campaignsCollection(), sendsCollection(), suppressionsCollection()}
}

func systemCollection(name, id string) *core.Collection {
	c := core.NewBaseCollection(name, id)
	c.System = true
	// nil rules: superusers only (also through the records API and realtime)
	c.ListRule, c.ViewRule, c.CreateRule, c.UpdateRule, c.DeleteRule = nil, nil, nil, nil, nil
	return c
}

func timestamps(prefix string) []core.Field {
	return []core.Field{
		&core.AutodateField{Id: prefix + "_created", Name: "created", OnCreate: true, System: true},
		&core.AutodateField{Id: prefix + "_updated", Name: "updated", OnCreate: true, OnUpdate: true, System: true},
	}
}

func campaignsCollection() *core.Collection {
	c := systemCollection(CampaignsCollection, CampaignsId)
	c.Fields.Add(
		&core.TextField{Id: "bmc_name", Name: "name", Required: true, Max: 200},
		&core.TextField{Id: "bmc_subject", Name: "subject", Max: 500},
		&core.EditorField{Id: "bmc_html", Name: "html", ConvertURLs: false},
		&core.TextField{Id: "bmc_text", Name: "text", Help: "Optional plain text body; generated from html when empty."},
		&core.TextField{Id: "bmc_from_name", Name: "from_name", Max: 200},
		&core.TextField{Id: "bmc_from_address", Name: "from_address", Max: 320},
		&core.JSONField{Id: "bmc_audience", Name: "audience", MaxSize: 1 << 16,
			Help: `{"collection":"users","filter":"verified = true","email_field":"email"} or {"sql":"SELECT email, id FROM users"}`},
		&core.JSONField{Id: "bmc_vars", Name: "vars", MaxSize: 1 << 16,
			Help: `{"name":"value"} or {"name":{"sql":"SELECT ... AS value"}}`},
		&core.SelectField{Id: "bmc_status", Name: "status", MaxSelect: 1,
			Values: []string{StatusDraft, StatusScheduled, StatusSending, StatusPaused, StatusDone, StatusCanceled}},
		&core.TextField{Id: "bmc_status_reason", Name: "status_reason", Max: 1000},
		&core.DateField{Id: "bmc_scheduled_at", Name: "scheduled_at"},
		&core.NumberField{Id: "bmc_rate_per_sec", Name: "rate_per_sec", Min: types.Pointer(0.0)},
		&core.NumberField{Id: "bmc_queued", Name: "queued", OnlyInt: true},
		&core.NumberField{Id: "bmc_sent", Name: "sent", OnlyInt: true},
		&core.NumberField{Id: "bmc_skipped", Name: "skipped", OnlyInt: true},
		&core.NumberField{Id: "bmc_failed", Name: "failed", OnlyInt: true},
		&core.TextField{Id: "bmc_test_to", Name: "test_to", Max: 1000},
		&core.DateField{Id: "bmc_started_at", Name: "started_at"},
		&core.DateField{Id: "bmc_finished_at", Name: "finished_at"},
	)
	c.Fields.Add(timestamps("bmc")...)
	c.AddIndex("idx_bulkmail_campaigns_status", false, "status", "")
	return c
}

func sendsCollection() *core.Collection {
	c := systemCollection(SendsCollection, SendsId)
	c.Fields.Add(
		&core.RelationField{Id: "bms_campaign", Name: "campaign", CollectionId: CampaignsId, MaxSelect: 1,
			Required: true, CascadeDelete: true},
		&core.TextField{Id: "bms_email", Name: "email", Required: true, Max: 320},
		&core.TextField{Id: "bms_record_ref", Name: "record_ref", Max: 300},
		&core.SelectField{Id: "bms_status", Name: "status", MaxSelect: 1,
			Values: []string{SendQueued, SendSent, SendSkipped, SendFailed}},
		&core.TextField{Id: "bms_reason", Name: "reason", Max: 2000},
		&core.NumberField{Id: "bms_attempts", Name: "attempts", OnlyInt: true},
		&core.DateField{Id: "bms_retry_at", Name: "retry_at"},
		&core.DateField{Id: "bms_sent_at", Name: "sent_at"},
		&core.TextField{Id: "bms_message_id", Name: "message_id", Max: 500},
	)
	c.Fields.Add(timestamps("bms")...)
	// the ledger: one row per campaign and address, so re-running a campaign never mails anyone twice
	c.AddIndex("idx_bulkmail_sends_campaign_email", true, "campaign, email", "")
	c.AddIndex("idx_bulkmail_sends_status", false, "status, campaign", "")
	c.AddIndex("idx_bulkmail_sends_sent_at", false, "sent_at", "")
	return c
}

func suppressionsCollection() *core.Collection {
	c := systemCollection(SuppressionsCollection, SuppressionsId)
	c.Fields.Add(
		&core.TextField{Id: "bmx_email", Name: "email", Required: true, Max: 320},
		&core.SelectField{Id: "bmx_reason", Name: "reason", MaxSelect: 1,
			Values: []string{ReasonUnsubscribed, ReasonBounced, ReasonComplained, ReasonManual}},
		&core.TextField{Id: "bmx_source", Name: "source", Max: 200},
		&core.TextField{Id: "bmx_campaign", Name: "campaign", Max: 100},
	)
	c.Fields.Add(timestamps("bmx")...)
	c.AddIndex("idx_bulkmail_suppressions_email", true, "email", "")
	return c
}

// EnsureSchema creates the bulk mail collections that don't exist yet. A collection that already exists (created by
// an earlier run, or by the legacy mothership's JS migration) must have the expected id and every expected field
// with the expected id and type; fields the plugin doesn't know are left alone. It's safe to call on every start.
func EnsureSchema(app core.App) error {
	for _, want := range Collections() {
		have, err := app.FindCollectionByNameOrId(want.Id)
		if err != nil {
			if byName, nameErr := app.FindCollectionByNameOrId(want.Name); nameErr == nil {
				return fmt.Errorf("bulkmail: collection %q exists with id %q, expected %q", want.Name, byName.Id, want.Id)
			}
			if err := app.Save(want); err != nil {
				return fmt.Errorf("bulkmail: create %s: %w", want.Name, err)
			}
			continue
		}
		if err := checkCollection(have, want); err != nil {
			return err
		}
	}
	return nil
}

func checkCollection(have, want *core.Collection) error {
	if have.Name != want.Name {
		return fmt.Errorf("bulkmail: collection %q is named %q, expected %q", want.Id, have.Name, want.Name)
	}
	var errs []error
	for _, wf := range want.Fields {
		hf := have.Fields.GetById(wf.GetId())
		if hf == nil {
			errs = append(errs, fmt.Errorf("missing field %q (%s)", wf.GetName(), wf.GetId()))
			continue
		}
		if hf.Type() != wf.Type() || hf.GetName() != wf.GetName() {
			errs = append(errs, fmt.Errorf("field %s is %s %q, expected %s %q",
				wf.GetId(), hf.Type(), hf.GetName(), wf.Type(), wf.GetName()))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("bulkmail: %s doesn't match the bulk mail schema: %w", want.Name, errors.Join(errs...))
	}
	return nil
}
