-- github-auto-label.lua
-- Automatically labels new issues based on keywords in the title.

sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.issue.opened" then
        return
    end

    local title = string.lower(event.payload.title or "")
    local owner = event.payload.owner
    local repo  = event.payload.repo
    local num   = event.payload.number

    if string.find(title, "bug") or string.find(title, "crash") then
        sekia.command("github-agent", "add_label", {
            owner  = owner,
            repo   = repo,
            number = num,
            label  = "bug",
        })
    end

    if string.find(title, "feature") or string.find(title, "request") then
        sekia.command("github-agent", "add_label", {
            owner  = owner,
            repo   = repo,
            number = num,
            label  = "enhancement",
        })
    end

    sekia.command("github-agent", "create_comment", {
        owner  = owner,
        repo   = repo,
        number = num,
        body   = "Thanks for opening this issue! A maintainer will review it shortly.",
    })

    sekia.log("info", "Processed new issue #" .. num .. ": " .. event.payload.title)
end)
