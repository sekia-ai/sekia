-- google-calendar-notify.lua
-- Example workflow: notify on new or upcoming calendar events.
-- Requires: sekia-google agent with calendar.enabled = true

sekia.on("sekia.events.google", function(event)
	if event.type == "google.calendar.event.created" then
		sekia.log("info", "New calendar event: " .. (event.payload.summary or "untitled"))
		-- Example: send a Slack message about the new event
		-- sekia.command("slack-agent", "send_message", {
		--   channel = "#calendar",
		--   text = "📅 New event: " .. event.payload.summary .. " at " .. event.payload.start,
		-- })
	end

	if event.type == "google.calendar.event.upcoming" then
		sekia.log("info", "Upcoming in " .. event.payload.minutes_until .. "m: " .. (event.payload.summary or "untitled"))
		-- Example: send a reminder
		-- sekia.command("slack-agent", "send_message", {
		--   channel = "#reminders",
		--   text = "⏰ Starting in " .. event.payload.minutes_until .. " min: " .. event.payload.summary,
		-- })
	end
end)
