-- gmail-auto-reply.lua
-- Automatically replies to emails with "urgent" in the subject.
-- Requires: sekia-google agent with gmail.enabled = true

sekia.on("sekia.events.google", function(event)
    if event.type ~= "gmail.message.received" then
        return
    end

    local subject = string.lower(event.payload.subject or "")
    local from = event.payload.from
    local thread_id = event.payload.thread_id
    local message_id = event.payload.message_id

    if string.find(subject, "urgent") or string.find(subject, "critical") then
        sekia.command("google-agent", "reply_email", {
            thread_id   = thread_id,
            in_reply_to = message_id,
            to          = from,
            subject     = "Re: " .. (event.payload.subject or ""),
            body        = "Thank you for your message. This has been flagged as urgent and will be reviewed promptly.",
        })

        sekia.log("info", "Auto-replied to urgent email from " .. from)
    end
end)
