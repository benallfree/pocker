package bulkmail

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// Audience picks a campaign's recipients from the app's own data, at queue time.
//
// Default: a collection and a PocketBase filter (same syntax as API rules). Advanced: a read-only SQL SELECT
// returning an email column (and optionally id plus any columns the template uses).
type Audience struct {
	Collection string `json:"collection,omitempty"`
	Filter     string `json:"filter,omitempty"`
	EmailField string `json:"email_field,omitempty"`
	SQL        string `json:"sql,omitempty"`
}

// Recipient is one resolved audience member.
type Recipient struct {
	Email     string
	RecordRef string         // "collection:id", empty for SQL rows without id
	Data      map[string]any // template data under "record"
}

const audiencePage = 500

// ParseAudience reads a campaign's audience JSON.
func ParseAudience(raw any) (Audience, error) {
	var a Audience
	var b []byte
	switch x := raw.(type) {
	case nil:
	case []byte:
		b = x
	case string:
		b = []byte(x)
	default:
		var err error
		if b, err = json.Marshal(x); err != nil {
			return a, err
		}
	}
	if len(b) > 0 && string(b) != "null" {
		if err := json.Unmarshal(b, &a); err != nil {
			return a, fmt.Errorf("invalid audience: %w", err)
		}
	}
	if a.SQL == "" && a.Collection == "" {
		return a, errors.New("audience needs a collection or a SQL query")
	}
	if a.EmailField == "" {
		a.EmailField = "email"
	}
	return a, nil
}

// NormalizeEmail lowercases and validates an address; it returns "" for anything that isn't one plain address.
func NormalizeEmail(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || !strings.Contains(s, "@") {
		return ""
	}
	return s
}

// EachRecipient calls fn for every audience member (duplicates and invalid addresses included; the caller skips
// them). Returning an error from fn stops the walk.
func (bm *BulkMail) EachRecipient(a Audience, fn func(Recipient) error) error {
	if a.SQL != "" {
		return bm.eachSQLRecipient(a.SQL, fn)
	}

	col, err := bm.app.FindCollectionByNameOrId(a.Collection)
	if err != nil {
		return fmt.Errorf("audience collection %q not found", a.Collection)
	}
	if !IsEmailFieldCandidate(col, col.Fields.GetByName(a.EmailField)) {
		return fmt.Errorf("collection %q has no usable email field %q", col.Name, a.EmailField)
	}

	// Collections page by id (keyset). A view's id isn't guaranteed unique or stable, so views page by offset; the
	// ledger's one-row-per-address rule absorbs duplicates if the view changes mid-walk.
	lastId, offset := "", 0
	for {
		filter := strings.TrimSpace(a.Filter)
		if !col.IsView() {
			if filter == "" {
				filter = "id > {:lastId}"
			} else {
				filter = "(" + filter + ") && id > {:lastId}"
			}
		}
		records, err := bm.app.FindRecordsByFilter(col, filter, "id", audiencePage, offset, dbx.Params{"lastId": lastId})
		if err != nil {
			return err
		}
		for _, r := range records {
			data := r.PublicExport()
			data["id"] = r.Id
			for _, f := range col.Fields { // hidden fields (email on auth collections) are part of the app's own data
				if f.Type() == core.FieldTypePassword || f.GetName() == core.FieldNameTokenKey {
					continue // never secrets
				}
				data[f.GetName()] = r.Get(f.GetName())
			}
			if err := fn(Recipient{Email: recordEmail(r, a.EmailField), RecordRef: col.Name + ":" + r.Id, Data: data}); err != nil {
				return err
			}
		}
		if len(records) < audiencePage {
			return nil
		}
		if col.IsView() {
			offset += len(records)
		} else {
			lastId = records[len(records)-1].Id
		}
	}
}

// IsEmailFieldCandidate reports whether f can hold a recipient's address: an email or text field, or on a view also
// a json field (SQLite expressions such as lower(email) come out as json columns).
func IsEmailFieldCandidate(col *core.Collection, f core.Field) bool {
	if f == nil || f.GetName() == core.FieldNameId || f.GetName() == core.FieldNameTokenKey {
		return false
	}
	switch f.Type() {
	case core.FieldTypeEmail, core.FieldTypeText:
		return true
	case core.FieldTypeJSON:
		return col.IsView()
	}
	return false
}

var sqlStartRe = regexp.MustCompile(`(?is)^\s*(select|with)\b`)

// QueryReadOnly runs a superuser-authored SELECT with SQLite's query_only pragma on, inside a transaction that is
// always rolled back, and returns every row.
func (bm *BulkMail) QueryReadOnly(sql string) ([]dbx.NullStringMap, error) {
	sql = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
	if !sqlStartRe.MatchString(sql) {
		return nil, errors.New("audience SQL must be a single SELECT")
	}
	if strings.Contains(sql, ";") {
		return nil, errors.New("audience SQL must be a single statement")
	}

	var rows []dbx.NullStringMap
	errRollback := errors.New("rollback")
	db, ok := bm.app.NonconcurrentDB().(*dbx.DB)
	if !ok {
		return nil, errors.New("audience SQL can't run inside a transaction")
	}
	err := db.Transactional(func(tx *dbx.Tx) error {
		if _, err := tx.NewQuery("PRAGMA query_only = ON").Execute(); err != nil {
			return err
		}
		qerr := tx.NewQuery(sql).All(&rows)
		if _, err := tx.NewQuery("PRAGMA query_only = OFF").Execute(); err != nil {
			return err
		}
		if qerr != nil {
			return qerr
		}
		return errRollback
	})
	if err != nil && !errors.Is(err, errRollback) {
		return nil, err
	}
	return rows, nil
}

func (bm *BulkMail) eachSQLRecipient(sql string, fn func(Recipient) error) error {
	rows, err := bm.QueryReadOnly(sql)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, ok := row["email"]; !ok {
			return errors.New(`audience SQL must return an "email" column`)
		}
		data := make(map[string]any, len(row))
		for k, v := range row {
			if v.Valid {
				data[k] = v.String
			} else {
				data[k] = nil
			}
		}
		ref := ""
		if id, _ := data["id"].(string); id != "" {
			ref = "sql:" + id
		}
		if err := fn(Recipient{Email: row["email"].String, RecordRef: ref, Data: data}); err != nil {
			return err
		}
	}
	return nil
}

// CountAudience returns how many distinct, valid, non-suppressed addresses the audience matches today, plus up to
// sampleSize of them.
func (bm *BulkMail) CountAudience(a Audience, sampleSize int) (count int, suppressed int, sample []string, err error) {
	seen := map[string]bool{}
	sup, err := bm.suppressedSet()
	if err != nil {
		return 0, 0, nil, err
	}
	err = bm.EachRecipient(a, func(r Recipient) error {
		e := NormalizeEmail(r.Email)
		if e == "" || seen[e] {
			return nil
		}
		seen[e] = true
		if sup[e] {
			suppressed++
			return nil
		}
		count++
		if len(sample) < sampleSize {
			sample = append(sample, e)
		}
		return nil
	})
	return count, suppressed, sample, err
}

func (bm *BulkMail) suppressedSet() (map[string]bool, error) {
	var emails []string
	err := bm.app.DB().Select("email").From(SuppressionsCollection).Column(&emails)
	set := make(map[string]bool, len(emails))
	for _, e := range emails {
		set[e] = true
	}
	return set, err
}

// resolveVars computes a campaign's vars once: plain values as is, {"sql": "..."} as the first row's value column.
func (bm *BulkMail) resolveVars(campaign *core.Record) (map[string]any, error) {
	bm.mu.Lock()
	if v, ok := bm.vars[campaign.Id]; ok {
		bm.mu.Unlock()
		return v, nil
	}
	bm.mu.Unlock()

	out := map[string]any{}
	raw := map[string]any{}
	if b := []byte(campaign.GetString("vars")); len(b) > 0 && string(b) != "null" {
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("invalid vars: %w", err)
		}
	}
	for name, v := range raw {
		if m, ok := v.(map[string]any); ok {
			if q, ok := m["sql"].(string); ok {
				rows, err := bm.QueryReadOnly(q)
				if err != nil {
					return nil, fmt.Errorf("var %q: %w", name, err)
				}
				if len(rows) > 0 {
					if val, ok := rows[0]["value"]; ok && val.Valid {
						out[name] = val.String
						continue
					}
				}
				out[name] = ""
				continue
			}
		}
		out[name] = v
	}

	bm.mu.Lock()
	bm.vars[campaign.Id] = out
	bm.mu.Unlock()
	return out, nil
}

func (bm *BulkMail) forgetVars(campaignId string) {
	bm.mu.Lock()
	delete(bm.vars, campaignId)
	bm.mu.Unlock()
}

// recordEmail reads the address from a record field; a view's json column holds a JSON string.
func recordEmail(r *core.Record, field string) string {
	if raw, ok := r.Get(field).(types.JSONRaw); ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
		return ""
	}
	return r.GetString(field)
}
