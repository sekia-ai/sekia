-- linear-auto-triage.lua
-- Automatically comments on newly created Linear issues.

sekia.on("sekia.events.linear", function(event)
    if event.type ~= "linear.issue.created" then
        return
    end

    local id = event.payload.id
    local title = event.payload.title
    local team = event.payload.team or "unknown"

    sekia.command("linear-agent", "create_comment", {
        issue_id = id,
        body     = "This issue has been automatically triaged. Team: " .. team,
    })

    sekia.log("info", "Auto-triaged Linear issue: " .. title)
end)
