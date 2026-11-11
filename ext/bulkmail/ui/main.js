// Bulk mail screens for the PocketBase dashboard. Loaded by the dashboard as a UI extension
// (/_/extensions/bulkmail/main.js). A thin view: every action calls the plugin's Go routes or the records API.
(() => {
    const C = "_bulkmail_campaigns";
    const S = "_bulkmail_sends";
    const X = "_bulkmail_suppressions";

    const statusClass = {
        draft: "",
        scheduled: "info",
        sending: "warning",
        paused: "warning",
        done: "success",
        canceled: "danger",
    };

    app.store.headerLinks.splice(Math.max(app.store.headerLinks.length - 1, 0), 0, {
        href: "#/bulkmail",
        icon: "ri-mail-send-line",
        label: "Mail",
    });

    function api(path, method = "POST", body) {
        return app.pb.send("/api/bulkmail" + path, { method, body });
    }

    function fail(err) {
        app.checkApiError(err);
    }

    function sidebar() {
        const links = [
            ["#/bulkmail", "ri-mail-send-line", "Campaigns"],
            ["#/bulkmail/suppressions", "ri-mail-forbid-line", "Suppressions"],
        ];
        return app.components.pageSidebar(
            { className: "bulkmail-sidebar" },
            t.nav(
                { className: "sidebar-content scrollable" },
                t.details(
                    { className: "nav-group", open: true },
                    t.summary({ tabIndex: -1, onfocusout: () => false, onclick: () => false, onkeyup: () => false }, "Bulk mail"),
                    ...links.map(([href, icon, label]) =>
                        t.a(
                            {
                                href,
                                className: () => {
                                    const h = window.location.hash;
                                    const active = href === "#/bulkmail"
                                        ? (h === "#/bulkmail" || h.startsWith("#/bulkmail/campaigns"))
                                        : h.startsWith(href);
                                    return `nav-item ${active ? "active" : ""}`;
                                },
                            },
                            t.i({ className: icon, ariaHidden: true }),
                            t.span({ className: "txt" }, label),
                        )
                    ),
                ),
            ),
        );
    }

    // layout mirrors the stock pages: breadcrumbs with secondary (icon) and primary buttons in the header, a
    // full-width body for lists (like Collections) or a centered wrapper for forms (like Settings), and the
    // footer with an optional total and the credits.
    function layout({ crumbs, secondary = [], primary = [], wide = false, total = null }, ...content) {
        return t.div(
            { className: "page page-bulkmail" },
            sidebar(),
            t.div(
                { className: "page-content full-height" },
                t.header(
                    { className: "page-header flex-nowrap" },
                    t.nav({ className: "breadcrumbs" }, ...crumbs.map((c) => t.div({ className: "breadcrumb-item" }, c))),
                    secondary.length ? t.div({ className: "page-header-secondary-btns" }, ...secondary) : null,
                    primary.length ? t.div({ className: "page-header-primary-btns" }, ...primary) : null,
                ),
                wide ? content : t.div({ className: "wrapper m-b-base" }, ...content),
                t.footer({ className: "page-footer" },
                    total ? t.span({ className: "total-count" }, "Total: ", total) : null,
                    app.components.credits(),
                ),
            ),
        );
    }

    function emptyRow(text, ...children) {
        return t.tr(null, t.td({ colSpan: 99, style: "height:59px" },
            t.div({ className: "sticky-content txt-center txt-hint" }, t.div({ className: "txt-bold" }, text), ...children)));
    }

    function date(value) {
        return value ? app.components.formattedDate({ value, short: true }) : t.span({ className: "txt-hint" }, "N/A");
    }

    function statusLabel(c) {
        return t.span({ className: `label ${statusClass[c.status] || ""}` }, c.status || "draft");
    }

    function progress(c) {
        const total = (c.queued || 0) + (c.sent || 0) + (c.skipped || 0) + (c.failed || 0);
        const pct = total ? Math.round(((c.sent + c.skipped + c.failed) / total) * 100) : 0;
        return t.span({ className: "txt-hint" }, `${c.sent || 0} sent · ${c.queued || 0} queued · ${c.skipped || 0} skipped · ${c.failed || 0} failed${total ? ` (${pct}%)` : ""}`);
    }

    // ---------------------------------------------------------------------------------------------------------
    // campaigns list

    app.routes.superuserOnly("#/bulkmail", () => {
        app.store.title = "Mail";
        const data = store({ loading: true, items: [] });

        async function load() {
            data.loading = true;
            try {
                data.items = await app.pb.collection(C).getFullList({ sort: "-created" });
            } catch (err) {
                fail(err);
            }
            data.loading = false;
        }

        async function create() {
            try {
                const c = await app.pb.collection(C).create({
                    name: "Untitled campaign",
                    status: "draft",
                    audience: { collection: "users", filter: "verified = true", email_field: "email" },
                    vars: {},
                });
                window.location.hash = "#/bulkmail/campaigns/" + c.id;
            } catch (err) {
                fail(err);
            }
        }

        load();

        const newButton = (cls) => t.button({ type: "button", className: cls, onclick: create },
            t.i({ className: "ri-add-line", ariaHidden: true }), t.span({ className: "txt" }, "New campaign"));

        return layout({
            crumbs: ["Mail", "Campaigns"],
            secondary: [app.components.refreshButton({ onclick: load })],
            primary: [newButton("btn")],
            wide: true,
            total: () => data.items.length,
        },
            t.div({ className: "page-table-wrapper m-t-sm" },
                t.table({ className: "responsive-table" },
                    t.thead({ className: "sticky" }, t.tr(null,
                        t.th(null, "Name"), t.th(null, "Status"), t.th(null, "Progress"), t.th(null, "Updated"))),
                    t.tbody(null, () => {
                        if (!data.items.length) {
                            return data.loading
                                ? t.tr(null, t.td({ colSpan: 99, style: "height:59px" }, t.span({ className: "skeleton-loader" })))
                                : emptyRow("No campaigns yet.", newButton("btn secondary expanded-lg m-t-10"));
                        }
                        return data.items.map((c) => {
                            const open = () => (window.location.hash = "#/bulkmail/campaigns/" + c.id);
                            return t.tr({ tabIndex: 0, className: "handle", onclick: open,
                                onkeypress: (e) => { if (e.key == "Enter" || e.key == " ") { e.preventDefault(); open(); } } },
                                t.td({ className: "txt-bold" }, c.name),
                                t.td(null, statusLabel(c)),
                                t.td(null, progress(c)),
                                t.td(null, date(c.updated)),
                            );
                        });
                    }),
                ),
            ),
        );
    });

    // ---------------------------------------------------------------------------------------------------------
    // campaign editor and progress

    app.routes.superuserOnly("#/bulkmail/campaigns/{id}", (route) => {
        const id = route.params.id;
        app.store.title = "Campaign";
        const data = store({
            loading: true,
            busy: false,
            c: null,
            useSQL: false,
            audienceJSON: "",
            varsJSON: "{}",
            count: null,
            preview: null,
            ledger: [],
            ledgerFilter: "",
            collections: [],
        });
        let unsubscribe;

        function editable() {
            return data.c && ["draft", "paused", "done", "scheduled"].includes(data.c.status);
        }

        function init(c) {
            data.c = c;
            const a = c.audience || {};
            data.useSQL = !!a.sql;
            data.audienceJSON = JSON.stringify(a);
            data.varsJSON = JSON.stringify(c.vars || {}, null, 2);
            app.store.title = c.name;
        }

        async function load() {
            try {
                init(await app.pb.collection(C).getOne(id));
                data.collections = (await app.pb.collections.getFullList({ filter: "type != 'view'" }))
                    .filter((c) => !c.name.startsWith("_"));
                unsubscribe = await app.pb.collection(C).subscribe(id, (e) => {
                    if (e.action === "update") {
                        Object.assign(data.c, {
                            status: e.record.status, status_reason: e.record.status_reason,
                            queued: e.record.queued, sent: e.record.sent, skipped: e.record.skipped, failed: e.record.failed,
                        });
                    }
                });
                loadLedger();
            } catch (err) {
                fail(err);
            }
            data.loading = false;
        }

        async function loadLedger() {
            try {
                const res = await app.pb.collection(S).getList(1, 50, {
                    filter: app.pb.filter("campaign = {:id}" + (data.ledgerFilter ? " && status = {:st}" : ""), { id, st: data.ledgerFilter }),
                    sort: "-updated",
                });
                data.ledger = res.items;
            } catch (err) {
                fail(err);
            }
        }

        function audience() {
            try {
                return JSON.parse(data.audienceJSON || "{}");
            } catch (_) {
                return {};
            }
        }
        function setAudience(patch) {
            data.audienceJSON = JSON.stringify({ ...audience(), ...patch });
            data.count = null;
        }

        async function save() {
            let vars;
            try {
                vars = JSON.parse(data.varsJSON || "{}");
            } catch (_) {
                app.toasts.error("Vars must be valid JSON.");
                return false;
            }
            const a = audience();
            if (data.useSQL) {
                delete a.collection; delete a.filter;
            } else {
                delete a.sql;
            }
            try {
                init(await app.pb.collection(C).update(id, {
                    name: data.c.name, subject: data.c.subject, html: data.c.html, text: data.c.text,
                    from_name: data.c.from_name, from_address: data.c.from_address,
                    test_to: data.c.test_to, rate_per_sec: data.c.rate_per_sec || 0,
                    scheduled_at: data.c.scheduled_at || "",
                    audience: a, vars,
                }));
                return true;
            } catch (err) {
                fail(err);
                return false;
            }
        }

        async function run(label, fn) {
            if (data.busy) return;
            data.busy = true;
            try {
                await fn();
            } catch (err) {
                fail(err);
            }
            data.busy = false;
        }

        const doSave = () => run("save", async () => { if (await save()) app.toasts.success("Saved."); });
        const doCount = () => run("count", async () => {
            data.count = await api("/audience/count", "POST", { audience: audience() });
        });
        const doPreview = () => run("preview", async () => {
            if (await save()) data.preview = await api(`/campaigns/${id}/preview`);
        });
        const doTest = () => run("test", async () => {
            if (await save()) {
                await api(`/campaigns/${id}/test`);
                app.toasts.success("Test sent to " + (data.c.test_to || "the test address") + ".");
            }
        });
        const action = (name, confirmText) => () => {
            const go = () => run(name, async () => {
                if (name === "start" && !(await save())) return;
                init(await api(`/campaigns/${id}/${name}`));
                loadLedger();
            });
            if (confirmText) {
                app.modals.confirm(confirmText, go);
            } else {
                go();
            }
        };

        function field(label, input, hint) {
            return t.div({ className: "field" }, t.label({}, label), input, hint ? t.div({ className: "field-help" }, hint) : null);
        }
        function text(key, attrs = {}) {
            return t.input({
                type: "text", ...attrs,
                disabled: () => !editable(),
                value: () => data.c[key] ?? "",
                oninput: (e) => (data.c[key] = e.target.value),
            });
        }

        load();

        return t.div({ className: "page-bulkmail-campaign", onunmount: () => unsubscribe?.() },
            layout({
                crumbs: [t.a({ href: "#/bulkmail" }, "Mail"), () => app.store.title],
                primary: [
                    () => !data.c ? null : (["draft", "done"].includes(data.c.status)
                                ? t.button({ type: "button", className: "btn success", onclick: action("start", data.c.status === "done"
                                    ? "Send to new matches? Addresses already in the ledger aren't mailed again."
                                    : "Start sending this campaign to its audience?") },
                                t.i({ className: "ri-send-plane-line" }), t.span({ className: "txt" }, data.c.scheduled_at ? "Schedule" : "Send"))
                                : null),
                    () => !data.c ? null : (["sending", "scheduled"].includes(data.c.status)
                                ? t.button({ type: "button", className: "btn warning", onclick: action("pause") }, t.span({ className: "txt" }, "Pause"))
                                : null),
                    () => !data.c ? null : (data.c.status === "paused"
                                ? t.button({ type: "button", className: "btn success", onclick: action("resume") }, t.span({ className: "txt" }, "Resume"))
                                : null),
                    () => !data.c ? null : (["sending", "scheduled", "paused"].includes(data.c.status)
                                ? t.button({ type: "button", className: "btn danger transparent", onclick: action("cancel", "Cancel this campaign? Queued recipients won't be mailed.") }, t.span({ className: "txt" }, "Cancel"))
                                : null),
                ],
            },
                () => {
                    if (data.loading || !data.c) {
                        return t.div({ className: "block txt-center" }, t.span({ className: "loader lg" }));
                    }
                    const c = data.c;
                    return t.div(
                        { className: "grid", inert: () => data.busy },

                        // status bar
                        t.div(
                            { className: "col-lg-12 flex flex-wrap gap-sm" },
                            () => statusLabel(data.c),
                            () => progress(data.c),
                            () => data.c.status_reason ? t.span({ className: "txt-hint" }, "· " + data.c.status_reason) : null,
                        ),

                        // content
                        t.div({ className: "col-lg-6" }, field("Name", text("name", { required: true }))),
                        t.div({ className: "col-lg-6" }, field("Subject", text("subject"), "Placeholders: {{record.field}}, {{campaign.name}}, vars.")),
                        t.div({ className: "col-lg-6" }, field("From name", text("from_name", { placeholder: () => app.store.settings?.meta?.senderName || "" }), "Empty uses the sender in Settings > Mail.")),
                        t.div({ className: "col-lg-6" }, field("From address", text("from_address", { type: "email", placeholder: () => app.store.settings?.meta?.senderAddress || "" }))),
                        t.div({ className: "col-lg-12" }, field("Body",
                            app.components.tinymce({
                                value: () => c.html || "",
                                disabled: () => !editable(),
                                onchange: (v) => (data.c.html = v),
                            }),
                            "{{record.name}} per recipient, {{if record.field}}…{{else}}…{{end}} blocks, {{unsubscribe_url}}. An unsubscribe footer is always added.",
                        )),

                        // audience
                        t.div({ className: "col-lg-12" },
                            t.div({ className: "flex gap-sm" },
                                t.h6({}, "Audience"),
                                t.div({ className: "flex-fill" }),
                                t.button({ type: "button", className: () => `btn sm ${data.useSQL ? "transparent" : "secondary"}`, onclick: () => (data.useSQL = false) }, "Filter"),
                                t.button({ type: "button", className: () => `btn sm ${data.useSQL ? "secondary" : "transparent"}`, onclick: () => (data.useSQL = true) }, "SQL"),
                            ),
                        ),
                        () => data.useSQL
                            ? t.div({ className: "col-lg-12" }, field("Read-only SELECT",
                                t.textarea({
                                    rows: 4, className: "txt-mono", disabled: () => !editable(),
                                    value: () => audience().sql || "",
                                    oninput: (e) => setAudience({ sql: e.target.value }),
                                }),
                                "Must return an email column; id and any other columns are available as {{record.column}}.",
                            ))
                            : [
                                t.div({ className: "col-lg-4" }, field("Collection",
                                    app.components.select({
                                        disabled: () => !editable(),
                                        options: () => data.collections.map((col) => ({ value: col.name, label: col.name })),
                                        value: () => audience().collection || "",
                                        onchange: (sel) => setAudience({ collection: sel?.[0]?.value || "" }),
                                    }),
                                )),
                                t.div({ className: "col-lg-6" }, field("Filter",
                                    t.input({
                                        type: "text", disabled: () => !editable(), placeholder: "verified = true",
                                        value: () => audience().filter || "",
                                        oninput: (e) => setAudience({ filter: e.target.value }),
                                    }),
                                    "Same syntax as API rules.",
                                )),
                                t.div({ className: "col-lg-2" }, field("Email field",
                                    t.input({
                                        type: "text", disabled: () => !editable(),
                                        value: () => audience().email_field || "email",
                                        oninput: (e) => setAudience({ email_field: e.target.value }),
                                    }),
                                )),
                            ],
                        t.div({ className: "col-lg-12 flex gap-sm" },
                            t.button({ type: "button", className: "btn secondary sm", onclick: doCount }, "Count recipients"),
                            () => data.count
                                ? t.span({ className: "txt-hint" }, `${data.count.count} recipients (${data.count.suppressed} suppressed)${data.count.sample?.length ? ": " + data.count.sample.join(", ") + "…" : ""}`)
                                : null,
                        ),

                        // options
                        t.div({ className: "col-lg-12" }, field("Vars (JSON)",
                            t.textarea({
                                rows: 3, className: "txt-mono", disabled: () => !editable(),
                                value: () => data.varsJSON,
                                oninput: (e) => (data.varsJSON = e.target.value),
                            }),
                            `{"promo": "X"} or {"count": {"sql": "SELECT COUNT(*) AS value FROM users"}}, computed once when sending starts.`,
                        )),
                        t.div({ className: "col-lg-4" }, field("Test address", text("test_to", { placeholder: "you@example.com" }))),
                        t.div({ className: "col-lg-4" }, field("Schedule (UTC)", t.input({
                            type: "datetime-local", disabled: () => !editable(),
                            value: () => (data.c.scheduled_at || "").replace(" ", "T").slice(0, 16),
                            onchange: (e) => (data.c.scheduled_at = e.target.value ? e.target.value.replace("T", " ") + ":00.000Z" : ""),
                        }), "Empty sends right away.")),
                        t.div({ className: "col-lg-4" }, field("Max per second", t.input({
                            type: "number", min: 0, step: "any", disabled: () => !editable(),
                            value: () => data.c.rate_per_sec || "",
                            oninput: (e) => (data.c.rate_per_sec = parseFloat(e.target.value) || 0),
                        }), "Can only lower the app's limit.")),

                        t.div({ className: "col-lg-12" }, t.hr()),
                        t.div({ className: "col-lg-12 flex gap-sm" },
                            t.button({ type: "button", className: "btn outline", onclick: doPreview },
                                t.i({ className: "ri-eye-line", ariaHidden: true }), t.span({ className: "txt" }, "Preview")),
                            t.button({ type: "button", className: "btn outline", onclick: doTest },
                                t.i({ className: "ri-mail-send-line", ariaHidden: true }), t.span({ className: "txt" }, "Send test")),
                            t.div({ className: "m-r-auto" }),
                            t.button({ type: "button", className: "btn expanded", disabled: () => !editable(), onclick: doSave },
                                t.span({ className: "txt" }, "Save changes")),
                        ),

                        () => data.preview
                            ? t.div({ className: "col-lg-12" },
                                t.p({}, t.strong({}, data.preview.subject), t.span({ className: "txt-hint" }, ` · to ${data.preview.to} · from ${data.preview.from}`)),
                                t.iframe({ className: "bulkmail-preview", sandbox: "", srcdoc: data.preview.html || "<pre>" + data.preview.text + "</pre>", style: "width:100%;height:420px;border:1px solid var(--surfaceAlt2Color);border-radius:var(--borderRadius);background:#fff" }),
                            )
                            : null,

                        // ledger
                        t.div({ className: "col-lg-12 flex gap-sm m-t-base" },
                            t.h6({}, "Recipients"),
                            t.div({ className: "flex-fill" }),
                            app.components.refreshButton({ className: "btn sm transparent secondary circle rotate-btn", onclick: loadLedger }),
                            t.div({ style: "width:160px" }, app.components.select({
                                options: ["", "queued", "sent", "skipped", "failed"].map((v) => ({ value: v, label: v || "All statuses" })),
                                value: () => data.ledgerFilter,
                                onchange: (sel) => { data.ledgerFilter = sel?.[0]?.value || ""; loadLedger(); },
                            })),
                        ),
                        t.div({ className: "col-lg-12" }, t.table({ className: "responsive-table" },
                            t.thead(null, t.tr(null, t.th(null, "Email"), t.th(null, "Status"), t.th(null, "Reason"), t.th(null, "Sent"))),
                            t.tbody(null, () => !data.ledger.length
                                ? emptyRow(data.ledgerFilter ? `No ${data.ledgerFilter} recipients.` : "Nobody queued yet.")
                                : data.ledger.map((s) => t.tr(null,
                                    t.td(null, s.email),
                                    t.td(null, t.span({ className: `label ${s.status === "sent" ? "success" : s.status === "failed" ? "danger" : ""}` }, s.status)),
                                    t.td({ className: "txt-hint" }, s.reason || ""),
                                    t.td(null, date(s.sent_at)),
                                ))),
                        )),
                    );
                },
            ),
        );
    });

    // ---------------------------------------------------------------------------------------------------------
    // suppressions

    app.routes.superuserOnly("#/bulkmail/suppressions", () => {
        app.store.title = "Suppressions";
        const data = store({ collection: null, filter: "", sort: "-created", reset: 0, total: 0 });

        async function load() {
            try {
                data.collection = await app.pb.collections.getOne(X);
            } catch (err) {
                fail(err);
            }
            count();
        }
        async function count() {
            try {
                data.total = (await app.pb.collection(X).getList(1, 1, { fields: "id", filter: data.filter, skipTotal: false })).totalItems;
            } catch (err) {
                if (!err.isAbort) fail(err);
            }
        }
        const refresh = () => {
            data.reset = Date.now();
            count();
        };
        const edit = (record = null) => app.modals.openRecordUpsert(data.collection, record, {
            onsave: refresh,
            ondelete: refresh,
        });
        async function exportCSV() {
            try {
                const items = await app.pb.collection(X).getFullList({ sort: "-created", filter: data.filter });
                const rows = [["email", "reason", "source", "campaign", "created"], ...items.map((s) => [s.email, s.reason, s.source, s.campaign, s.created])];
                const blob = new Blob([rows.map((r) => r.map((v) => `"${String(v ?? "").replaceAll('"', '""')}"`).join(",")).join("\n")], { type: "text/csv" });
                const a = document.createElement("a");
                a.href = URL.createObjectURL(blob);
                a.download = "suppressions.csv";
                a.click();
            } catch (err) {
                fail(err);
            }
        }

        load();

        return layout({
            crumbs: ["Mail", "Suppressions"],
            secondary: [app.components.refreshButton({ onclick: refresh })],
            primary: [
                t.button({ type: "button", className: "btn outline", onclick: exportCSV },
                    t.i({ className: "ri-download-line", ariaHidden: true }), t.span({ className: "txt" }, "Export CSV")),
                t.button({ type: "button", className: "btn", disabled: () => !data.collection, onclick: () => edit() },
                    t.i({ className: "ri-add-line", ariaHidden: true }), t.span({ className: "txt" }, "Add address")),
            ],
            wide: true,
            total: () => data.total,
        },
            t.p({ className: "txt-hint m-b-sm" }, "Addresses here never receive campaign mail, whatever the audience says. Unsubscribes, bounces and complaints are added automatically."),
            () => !data.collection ? null : [
                app.components.recordsSearchbar({
                    collection: () => data.collection,
                    value: () => data.filter,
                    onsubmit: (v) => { data.filter = v; count(); },
                }),
                app.components.recordsList({
                    className: "m-t-sm",
                    reset: () => data.reset,
                    collection: () => data.collection,
                    filter: () => data.filter,
                    sort: () => data.sort,
                    onselect: (record) => edit(record),
                    onchange: (filter, sort) => { data.filter = filter; data.sort = sort; count(); },
                }),
            ],
        );
    });

    // ---------------------------------------------------------------------------------------------------------
    // settings: a "Bulk mail" section under the form on Settings > Mail (the sender itself is the app's, above it)

    app.routes.superuserOnly("#/bulkmail/settings", () => {
        setTimeout(() => window.location.replace("#/settings/mail"));
        return t.div();
    });

    document.addEventListener("mount:mailSettingsForm", (e) => {
        const form = e.detail;
        form.parentElement?.querySelector(":scope > .bulkmail-settings")?.remove();
        form.after(bulkmailSettings());
    });

    function bulkmailSettings() {
        const data = store({ loading: true, saving: false, s: null, webhookSecret: "", initSerialized: "", showFeedback: false });

        function snapshot() {
            return JSON.stringify([data.s, data.webhookSecret]);
        }
        async function load() {
            try {
                data.s = await api("/settings", "GET");
                data.initSerialized = snapshot();
            } catch (err) {
                fail(err);
            }
            data.loading = false;
        }
        async function save(e) {
            e.preventDefault();
            data.saving = true;
            try {
                data.s = await api("/settings", "PATCH", {
                    postalAddress: data.s.postalAddress,
                    unsubscribeMailto: data.s.unsubscribeMailto,
                    snsTopicArns: (data.s.snsTopicArns || []).filter(Boolean),
                    webhookSecret: data.webhookSecret,
                });
                data.webhookSecret = "";
                data.initSerialized = snapshot();
                app.toasts.success("Saved bulk mail settings.");
            } catch (err) {
                fail(err);
            }
            data.saving = false;
        }

        load();

        return t.div({ className: "bulkmail-settings m-t-base" }, () => {
            if (data.loading || !data.s) {
                return t.div({ className: "block txt-center" }, t.span({ className: "loader" }));
            }
            const s = data.s;
            return t.form({ className: "grid", inert: () => data.saving, onsubmit: save },
                t.div({ className: "col-lg-12" }, t.hr()),
                t.div({ className: "col-lg-12" },
                    t.h6({ className: "m-b-xs" }, "Bulk mail"),
                    s.allowed
                        ? t.p({ className: "txt-hint" }, `Campaigns send from the sender above. This app may send ${s.rate} messages per second and ${s.daily} per day (${s.sentToday} sent today, UTC).`)
                        : t.p({ className: "txt-danger" }, "Bulk mail isn't available for this app."),
                ),
                t.div({ className: "col-lg-12" }, t.div({ className: "field" }, t.label({}, "Postal address"),
                    t.textarea({ rows: 3, value: () => s.postalAddress || "", oninput: (e) => (s.postalAddress = e.target.value) }),
                    t.div({ className: "field-help" }, "Shown in every campaign footer (anti-spam laws require one)."))),
                t.div({ className: "col-lg-6" }, t.div({ className: "field" }, t.label({}, "Unsubscribe mailbox"),
                    t.input({ type: "email", value: () => s.unsubscribeMailto || "", oninput: (e) => (s.unsubscribeMailto = e.target.value) }),
                    t.div({ className: "field-help" }, "Optional mailto: alternative in the List-Unsubscribe header."))),
                t.div({ className: "col-lg-12" },
                    t.button({ type: "button", className: "btn sm secondary", onclick: () => (data.showFeedback = !data.showFeedback) },
                        t.span({ className: "txt" }, () => data.showFeedback ? "Hide bounces and complaints" : "Bounces and complaints"),
                        t.i({ className: () => data.showFeedback ? "ri-arrow-up-s-line" : "ri-arrow-down-s-line", ariaHidden: true }))),
                () => !data.showFeedback ? null : t.div({ className: "col-lg-12" }, t.div({ className: "grid" },
                    t.div({ className: "col-lg-12" }, t.div({ className: "field" }, t.label({}, "Amazon SES (SNS) endpoint"),
                        t.input({ type: "text", readOnly: true, value: s.sesEndpoint }),
                        t.div({ className: "field-help" }, "Subscribe this HTTPS URL to the SNS topic your SES identity sends bounce and complaint notifications to. Signatures are verified."))),
                    t.div({ className: "col-lg-12" }, t.div({ className: "field" }, t.label({}, "Allowed SNS topic ARNs"),
                        t.input({ type: "text", placeholder: "arn:aws:sns:us-east-1:123456789012:ses-feedback", value: () => (s.snsTopicArns || []).join(", "),
                            oninput: (e) => (s.snsTopicArns = e.target.value.split(",").map((v) => v.trim())) }),
                        t.div({ className: "field-help" }, "Comma separated. Empty accepts any topic with a valid AWS signature."))),
                    t.div({ className: "col-lg-12" }, t.div({ className: "field" }, t.label({}, "Generic webhook"),
                        t.input({ type: "text", readOnly: true, value: s.webhookEndpoint }),
                        t.div({ className: "field-help" }, `POST {"type":"bounce|complaint|unsubscribe","email":"…"} with an X-Bulkmail-Secret header.`))),
                    t.div({ className: "col-lg-6" }, t.div({ className: "field" }, t.label({}, "Webhook secret"),
                        t.input({ type: "password", autocomplete: "new-password", placeholder: s.hasWebhookSecret ? "•••••• (set; type to replace)" : "Not set: webhook disabled",
                            value: () => data.webhookSecret, oninput: (e) => (data.webhookSecret = e.target.value) }))),
                )),
                t.div({ className: "col-lg-12 flex" },
                    t.div({ className: "m-r-auto" }),
                    t.button({ type: "submit", className: "btn expanded", disabled: () => data.initSerialized == snapshot() },
                        t.span({ className: "txt" }, "Save bulk mail settings"))),
            );
        });
    }
})();
