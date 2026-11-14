// Settings → Mail note for PocketOne ext/mailrelay: tells the superuser where mail goes while SMTP is off.
// Plain DOM plus the dashboard's app.pb client, so it depends on as little of the experimental UI kit as possible.
(() => {
    let status = null;

    async function load() {
        try {
            status = await app.pb.send("/api/mailrelay");
        } catch {
            status = null;
        }
        return status;
    }

    function note(s) {
        const box = document.createElement("div");
        box.className = "alert alert-info m-b-sm";
        box.dataset.mailrelay = "1";
        if (!s.send) {
            box.className = "alert alert-warning m-b-sm";
            box.textContent = "Outgoing mail through the host is off for this instance. Turn on SMTP below to send mail.";
            return box;
        }
        box.textContent =
            `While SMTP is off, mail is sent through the host as ${s.address} ` +
            `(up to ${s.perMinute} per minute and ${s.daily} per day; ${s.sentToday} sent today). ` +
            `Turn on SMTP below to use your own server instead.`;
        return box;
    }

    // the settings form, so the note sits under the page title, next to the SMTP toggle it talks about
    document.addEventListener("mount:mailSettingsForm", async (e) => {
        const s = await load();
        if (!s || e.detail.querySelector("[data-mailrelay]")) return;
        e.detail.prepend(note(s));
    });
})();
