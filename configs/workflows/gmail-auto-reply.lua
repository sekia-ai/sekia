-- gmail-auto-reply.lua
-- Automatically replies to emails with "urgent" in the subject.

sekia.on("sekia.events.gmail", function(event)
    if event.type ~= "gmail.message.received" then
        return
    end

    local subject = string.lower(event.payload.subject or "")
    local from = event.payload.from
    local message_id = event.payload.message_id

    if string.find(subject, "urgent") or string.find(subject, "critical") then
        sekia.command("gmail-agent", "reply_email", {
            message_id = message_id,
            body       = "Thank you for your message. This has been flagged as urgent and will be reviewed promptly.",
        })

        sekia.log("info", "Auto-replied to urgent email from " .. from)
    end
end)
