// Loaded by the PocketBase dashboard (served at /_/extensions/hello/main.js, bundled into /_/extensions.js).
// Uses the dashboard's own UI kit (t.*) and globals (app.routes, app.store, app.pb).

// a new page with a header link
app.routes.superuserOnly("#/hello", async () => {
    const res = await app.pb.send("/api/hello");
    return t.div({ className: "page" },
        t.div({ className: "page-content" },
            t.h1({}, "Hello"),
            t.p({}, `This page comes from a Go extension running for instance ${res.instance}.`),
        ),
    );
});

app.store.headerLinks.push({ href: "#/hello", icon: "ri-emotion-happy-line", label: "Hello" });

// add to an existing screen: every screen marked with pbEvent fires mount:<name> with its element
document.addEventListener("mount:pageBackupsSettings", (e) => {
    e.detail.prepend(t.p({ className: "txt-hint" }, "Hello from an extension on the Backups screen."));
});
