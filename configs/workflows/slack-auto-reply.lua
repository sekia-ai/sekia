-- slack-auto-reply.lua
-- Automatically replies to messages mentioning the bot.

sekia.on("sekia.events.slack", function(event)
    if event.type ~= "slack.mention" then
        return
    end

    local channel = event.payload.channel
    local ts = event.payload.timestamp
    local user = event.payload.user

    sekia.command("slack-agent", "send_reply", {
        channel   = channel,
        thread_ts = ts,
        text      = "Hi <@" .. user .. ">, thanks for reaching out!",
    })

    sekia.log("info", "Replied to mention from " .. user .. " in " .. channel)
end)
