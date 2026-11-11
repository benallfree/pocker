// Point-in-time restore section on Settings → Backups (PocketBase CE ext/pitr).
// Served at /_/extensions/pitr/main.js and bundled into /_/extensions.js by the dashboard.
// Uses plain DOM plus the dashboard's app.pb client, so it depends on as little of the experimental UI kit as possible.

(() => {
    const el = (tag, props = {}, ...children) => {
        const node = document.createElement(tag);
        for (const [k, v] of Object.entries(props)) {
            if (k === "className") node.className = v;
            else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
            else node.setAttribute(k, v);
        }
        for (const c of children.flat()) {
            if (c == null || c === false) continue;
            node.append(c instanceof Node ? c : document.createTextNode(String(c)));
        }
        return node;
    };

    const fmt = (iso) => (iso ? new Date(iso).toLocaleString() : "");
    const size = (b) => (b < 1 << 20 ? `${Math.max(1, Math.round(b / 1024))} KiB` : `${(b / (1 << 20)).toFixed(1)} MiB`);
    // value for <input type="datetime-local"> in local time
    const localInput = (d) => {
        const p = (n) => String(n).padStart(2, "0");
        return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
    };

    async function waitUntilBack() {
        // the app restarts to swap the database in; poll until it answers again
        for (let i = 0; i < 120; i++) {
            await new Promise((r) => setTimeout(r, 1000));
            try {
                const st = await app.pb.send("/api/pitr", { requestKey: null });
                if (!st.restoring && !st.pending) return;
            } catch (_) {}
        }
    }

    async function restore(at, section) {
        const when = new Date(at).toLocaleString();
        if (!confirm(`Restore the database to ${when}?\n\nThe app restarts. Changes made after that moment are replaced; the current database is kept, and you can undo the restore.`)) {
            return;
        }
        section.replaceChildren(el("div", { className: "alert info" }, `Restoring to ${when}… the app restarts; this page reloads when it's back.`));
        try {
            await app.pb.send("/api/pitr/restore", { method: "POST", body: { at: new Date(at).toISOString() }, requestKey: null });
        } catch (err) {
            app.checkApiError?.(err);
            render(section);
            return;
        }
        await waitUntilBack();
        window.location.reload();
    }

    async function render(section) {
        let st;
        try {
            st = await app.pb.send("/api/pitr", { requestKey: null });
        } catch (err) {
            section.remove(); // plugin not enabled for this app
            return;
        }

        const where = st.destination === "bucket" ? `bucket${st.label ? ` (${st.label})` : ""}` : "this server's disk";
        const children = [
            el("div", { className: "flex gap-10 m-b-sm" }, el("div", { className: "txt-lg" }, "Point-in-time restore")),
        ];

        if (!st.window) {
            children.push(el("p", { className: "txt-hint" }, "History starts with the first change after point-in-time restore was turned on."));
            section.replaceChildren(...children);
            return;
        }

        children.push(
            el("p", { className: "txt-hint" },
                `Restorable from ${fmt(st.window.earliest)} to ${fmt(st.window.latest)}. `,
                `History: ${size(st.size)}, stored on ${where}, kept for ${st.retention}. `,
                "Covers the database (records, collections, settings), not uploaded files."),
        );

        const last = st.lastRestore;
        if (last) {
            children.push(
                el("div", { className: last.error ? "alert danger m-b-sm" : "alert info m-b-sm" },
                    last.error
                        ? `The last restore (to ${fmt(last.at)}) failed: ${last.error}`
                        : `Restored to ${fmt(last.at)} on ${fmt(last.restoredAt)}. `,
                    !last.error && el("button", {
                        type: "button",
                        className: "btn sm secondary",
                        onclick: () => restore(new Date(new Date(last.restoredAt).getTime() - 1000).toISOString(), section),
                    }, "Undo this restore"),
                ),
            );
        }

        const input = el("input", {
            type: "datetime-local",
            step: "1",
            min: localInput(new Date(st.window.earliest)),
            max: localInput(new Date()),
            value: localInput(new Date(Date.now() - 5 * 60 * 1000)),
        });
        children.push(
            el("form", {
                className: "flex gap-10",
                onsubmit: (e) => {
                    e.preventDefault();
                    if (input.value) restore(input.value, section);
                },
            },
                el("label", { className: "txt-bold" }, "Restore to"),
                input,
                el("button", { type: "submit", className: "btn warning", disabled: st.restoring || st.pending ? "" : null }, "Restore"),
            ),
        );
        section.replaceChildren(...children);
        section.querySelectorAll("[disabled='null']").forEach((b) => b.removeAttribute("disabled"));
    }

    document.addEventListener("mount:pageBackupsSettings", (e) => {
        const grid = e.detail.querySelector(".page-content .wrapper .grid") || e.detail;
        const section = el("div", { className: "col-lg-12 pitr-section" });
        grid.prepend(section);
        render(section);
    });
})();
