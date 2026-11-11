// API tokens screen for the PocketBase dashboard (Settings → API tokens). Loaded as a UI extension
// (/_/extensions/tokens/main.js). A thin view over the plugin's /api/tokens routes.
(() => {
    app.store.settingsNavGroups["Access"] = [
        ...(app.store.settingsNavGroups["Access"] || []),
        { href: "#/settings/tokens", icon: "ri-key-2-line", label: "API tokens" },
    ];

    function api(path, method = "GET", body) {
        return app.pb.send("/api/tokens" + path, { method, body });
    }

    function sidebar() {
        return app.components.pageSidebar(
            { className: "settings-sidebar" },
            t.nav({ className: "sidebar-content scrollable" }, () => {
                const out = [];
                for (const group in app.store.settingsNavGroups) {
                    out.push(t.details(
                        { className: "nav-group", open: true },
                        t.summary({ tabIndex: -1, onfocusout: () => false, onclick: () => false, onkeyup: () => false }, group),
                        ...app.store.settingsNavGroups[group].map((link) => t.a(
                            { href: link.href, className: () => `nav-item ${app.utils.isActivePath(link.href, false) ? "active" : ""}` },
                            link.icon ? t.i({ className: link.icon, ariaHidden: true }) : null,
                            t.span({ className: "txt" }, link.label),
                        )),
                    ));
                }
                return out;
            }),
        );
    }

    function date(v) {
        return v ? v.slice(0, 16) : "";
    }

    app.routes.superuserOnly("#/settings/tokens", () => {
        app.store.title = "API tokens";
        const data = store({
            loading: true,
            items: [],
            scopes: [],
            created: null, // { token, record } right after create
            form: { name: "", scopes: [], ttlDays: "", ownerCollection: "_superusers", owner: "" },
        });

        async function load() {
            try {
                const [list, sc] = await Promise.all([api(""), api("/scopes")]);
                data.items = list.items;
                data.scopes = sc.scopes;
            } catch (err) {
                app.checkApiError(err);
            }
            data.loading = false;
        }

        async function create(e) {
            e.preventDefault();
            const f = data.form;
            try {
                data.created = await api("", "POST", {
                    name: f.name,
                    scopes: f.scopes,
                    ttl: f.ttlDays ? Math.round(Number(f.ttlDays) * 86400) : 0,
                    ownerCollection: f.owner ? f.ownerCollection : "",
                    owner: f.owner.trim(),
                });
                data.form = { name: "", scopes: [], ttlDays: "", ownerCollection: "_superusers", owner: "" };
                load();
            } catch (err) {
                app.checkApiError(err);
            }
        }

        function revoke(tok) {
            app.modals.confirm(`Revoke "${tok.name || tok.prefix}"? Anything using it stops working.`, async () => {
                try {
                    await api("/" + tok.id, "DELETE");
                    app.toasts.success("Token revoked.");
                    load();
                } catch (err) {
                    app.checkApiError(err);
                }
            });
        }

        function toggleScope(name) {
            const s = data.form.scopes;
            data.form.scopes = s.includes(name) ? s.filter((x) => x !== name) : [...s, name];
        }

        load();

        return t.div(
            { className: "page page-tokens" },
            sidebar(),
            t.div(
                { className: "page-content full-height" },
                t.header({ className: "page-header" },
                    t.nav({ className: "breadcrumbs" },
                        t.div({ className: "breadcrumb-item" }, "Settings"),
                        t.div({ className: "breadcrumb-item" }, "API tokens"))),
                t.div(
                    { className: "wrapper m-b-base" },
                    t.p({ className: "txt-hint" },
                        "Long-lived bearer tokens (pbt_…) for CI, deploys and scripts. A token acts as its owner, limited to its scopes if it has any. ",
                        "Send it as ", t.code({}, "Authorization: Bearer pbt_…"), "."),
                    () => data.created ? t.div({ className: "alert success m-b-base" },
                        t.div({ className: "content" },
                            t.p({}, t.strong({}, "Copy the new token now. "), "It won't be shown again."),
                            t.div({ className: "flex gap-sm" },
                                t.code({ className: "txt-mono", style: "word-break:break-all" }, data.created.token),
                                app.components.copyButton ? app.components.copyButton(data.created.token) : null),
                        ),
                        t.button({ type: "button", className: "close", onclick: () => (data.created = null) }, t.i({ className: "ri-close-line" })),
                    ) : null,
                    t.form({ className: "grid m-b-base", onsubmit: create },
                        t.div({ className: "col-lg-4" }, t.div({ className: "field" }, t.label({}, "Name"),
                            t.input({ type: "text", required: true, placeholder: "ci", value: () => data.form.name, oninput: (e) => (data.form.name = e.target.value) }))),
                        t.div({ className: "col-lg-2" }, t.div({ className: "field" }, t.label({}, "Expires in (days)"),
                            t.input({ type: "number", min: 0, placeholder: "never", value: () => data.form.ttlDays, oninput: (e) => (data.form.ttlDays = e.target.value) }))),
                        t.div({ className: "col-lg-3" }, t.div({ className: "field" }, t.label({}, "Owner collection"),
                            t.input({ type: "text", value: () => data.form.ownerCollection, oninput: (e) => (data.form.ownerCollection = e.target.value) }))),
                        t.div({ className: "col-lg-3" }, t.div({ className: "field" }, t.label({}, "Owner (id or email)"),
                            t.input({ type: "text", placeholder: "me", value: () => data.form.owner, oninput: (e) => (data.form.owner = e.target.value) }))),
                        t.div({ className: "col-lg-12" }, () => data.scopes.length
                            ? t.div({ className: "flex gap-sm flex-wrap" },
                                t.span({ className: "txt-hint" }, "Scopes (none: full rights):"),
                                ...data.scopes.map((s) => t.label({ className: "flex gap-5", title: s.description || "" },
                                    t.input({ type: "checkbox", checked: () => data.form.scopes.includes(s.name), onchange: () => toggleScope(s.name) }),
                                    t.span({}, s.name))))
                            : t.span({ className: "txt-hint" }, "No scopes are registered: tokens get their owner's full rights.")),
                        t.div({ className: "col-lg-12" }, t.button({ type: "submit", className: "btn" }, "Create token")),
                    ),
                    () => data.loading
                        ? t.div({ className: "block txt-center" }, t.span({ className: "loader lg" }))
                        : !data.items.length
                            ? t.p({ className: "txt-hint" }, "No API tokens yet.")
                            : t.table({},
                                t.thead({}, t.tr({},
                                    t.th({}, "Name"), t.th({}, "Token"), t.th({}, "Owner"), t.th({}, "Scopes"),
                                    t.th({}, "Expires"), t.th({}, "Last used"), t.th({}, ""))),
                                t.tbody({}, ...data.items.map((tok) => t.tr({},
                                    t.td({ style: "white-space:nowrap" }, tok.name, tok.clientId ? t.span({ className: "label m-l-5" }, "oauth: " + tok.clientId) : null),
                                    t.td({ className: "txt-mono txt-hint" }, tok.prefix + "…"),
                                    t.td({}, tok.ownerEmail || tok.owner, t.span({ className: "txt-hint" }, " · " + tok.ownerCollection)),
                                    t.td({}, tok.scopes.length ? tok.scopes.map((s) => t.span({ className: "label m-r-5", style: "white-space:nowrap" }, s)) : t.span({ className: "txt-hint" }, "all")),
                                    t.td({ className: "txt-hint" }, date(tok.expires) || "never"),
                                    t.td({ className: "txt-hint" }, tok.lastUsed ? `${date(tok.lastUsed)} · ${tok.lastIp}` : "never"),
                                    t.td({ className: "txt-right" }, t.button({ type: "button", className: "btn transparent danger sm", onclick: () => revoke(tok) }, "Revoke")),
                                ))),
                            ),
                ),
                t.footer({ className: "page-footer" }, app.components.credits()),
            ),
        );
    });
})();
