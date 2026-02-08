-- ai-pr-summary.lua
-- Uses AI to generate a summary comment on new pull requests.

sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.pr.opened" then return end

    local prompt = "Write a brief one-paragraph summary of this pull request. Be concise and technical.\n\n"
        .. "Title: " .. (event.payload.title or "") .. "\n"
        .. "Body: " .. (event.payload.body or "")

    local result, err = sekia.ai(prompt, {
        max_tokens = 256,
        system = "You are a helpful code review assistant. Be concise and technical.",
    })

    if err then
        sekia.log("error", "AI summary failed: " .. err)
        return
    end

    sekia.command("github-agent", "create_comment", {
        owner  = event.payload.owner,
        repo   = event.payload.repo,
        number = event.payload.number,
        body   = "**AI Summary:** " .. result,
    })

    sekia.log("info", "AI summarized PR #" .. event.payload.number)
end)
